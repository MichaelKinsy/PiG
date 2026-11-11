// Ports packages/durable/test/examples/43-subagent-waits.ts: a subagent tool that parks until its child conversation
// answers, notified by the child's submission when it settles. The child itself parks on a person's answer, so while the
// person thinks, every task is parked and the Session needs no process. The script's printing is the transcript this
// test reads.
//
// Go mapping: the script's pauses of a few milliseconds become waits for the parked state and settledWake; upstream's
// built-in `SubmissionSettled` signal is durable.SubmissionSettledSignal.

package examples_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/schema"
)

// Pi: packages/durable/src/signals.ts (SubmissionSettled), packages/durable/src/harness/submissions.ts (notify).
func TestExample43SubagentWaits(t *testing.T) {
	// The child's tool: it asks a person and parks until the answer comes.
	answerSignal := durable.DefineSignal(durable.SignalDefinition[answerValue]{
		Name: "example.answer", Version: 1, Schema: schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}),
	})
	var askedMu sync.Mutex
	asked := map[durable.TaskId]string{}
	askedQuestions := func() ([]durable.TaskId, []string) {
		askedMu.Lock()
		defer askedMu.Unlock()
		var ids []durable.TaskId
		for id := range asked {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		var questions []string
		for _, id := range ids {
			questions = append(questions, asked[id])
		}
		return ids, questions
	}
	askPerson := &durable.ToolRegistration{
		Name:        "ask_person",
		Description: "Ask the person a question and wait for the answer",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"question": map[string]any{"type": "string"}},
			"required":   []any{"question"},
		},
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			question := args.(map[string]any)["question"].(string)
			state, err := durable.ReadSignal(ctx, api, answerSignal, "answer")
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if state != nil && !state.Expired {
				return textResult(state.Value.Text), nil
			}
			askedMu.Lock()
			asked[api.TaskId()] = question
			askedMu.Unlock()
			return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{Signals: []durable.SignalKey{answerSignal.Key("answer")}}}}, nil
		},
	}

	// The parent's tool: delegate to a subagent and park until its submission settles.
	var subagent *durable.Extension
	subagent = &durable.Extension{Name: "subagent", Tools: []*durable.ToolRegistration{{
		Name:        "subagent",
		Description: "Delegate a task to a subagent and get its answer back",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"task": map[string]any{"type": "string"}},
			"required":   []any{"task"},
		},
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			task := args.(map[string]any)["task"].(string)
			state, err := durable.ReadSignal(ctx, api, durable.SubmissionSettledSignal, "answered")
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if state != nil && !state.Expired {
				settled := state.Value
				if settled.Status != durable.SubmissionDone || settled.Type == durable.SubmissionTypeWrite {
					isError := true
					return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("Subagent ended: %s", settled.Status)}}, IsError: &isError}, nil
				}
				// Any entry by ID, read in a commit without writes.
				entry, err := api.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.Entry(*settled.Answer) })
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				message := entry.(*durable.EntryRecord).Model[0].(ai.AssistantMessage)
				var output []ai.ToolResultMessageContent
				for _, part := range message.Content {
					if text, ok := part.(ai.TextContent); ok {
						output = append(output, text)
					}
				}
				return durable.ToolExecutionResult{Output: output}, nil
			}
			// The parent's own selection without this extension, computed before the commit.
			agent, err := api.Agent(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			var extensions []*durable.Extension
			for _, extension := range agent.Extensions {
				if extension.Name != subagent.Name {
					extensions = append(extensions, extension)
				}
			}
			created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				taskId := api.TaskId()
				existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &taskId}, 1, nil)
				if err != nil {
					return nil, err
				}
				if len(existing.Items) > 0 {
					return existing.Items[0].Id, nil
				}
				child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskId}})
				if err != nil {
					return nil, err
				}
				return child.Id, harness.Configure(tx, child.Id, harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{List: extensions})})
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			handle, err := api.Conversation(ctx, created.(durable.ConversationId))
			if err != nil || handle == nil {
				return durable.ToolExecutionResult{}, fmt.Errorf("child conversation: %w", err)
			}
			// The request ID makes a rerun find the same submission; `Notify` signals this call when it settles.
			if _, err := handle.Submit(ctx, durable.SubmissionDraft{
				Type:      durable.SubmissionTypeInput,
				Content:   ai.UserText(task),
				RequestId: new(fmt.Sprintf("subagent:%d", api.TaskId())),
				Notify:    &durable.SubmissionNotify{TaskId: api.TaskId(), Key: "answered"},
			}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{Signals: []durable.SignalKey{durable.SubmissionSettledSignal.Key("answered")}}}}, nil
		},
	}}}

	models := fauxModels(
		// Parent: delegate.
		fauxToolTurn("subagent", map[string]any{"task": "Find out whether we may deploy today"}, "delegate"),
		// Child: ask the person, then answer.
		fauxToolTurn("ask_person", map[string]any{"question": "May we deploy today?"}, "ask"),
		fauxAnswer("The person says yes, after 5 pm."),
		// Parent: final answer.
		fauxAnswer("We may deploy today, after 5 pm."),
	)
	registry := harness.CreateRegistry()
	installed(t, registry, &durable.Extension{Name: "people", Tools: []*durable.ToolRegistration{askPerson}})
	installed(t, registry, subagent)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry, OnWake: func(harness.Wake) {}})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Can we deploy today?")}))

	var script printed
	var questionIds []durable.TaskId
	var questions []string
	eventually(t, func() bool {
		questionIds, questions = askedQuestions()
		return len(questionIds) > 0
	})
	// Parked everywhere: the child's tool call, and the parent's call on the child's submission.
	waitSleeping(t, opened, questionIds[0])
	eventually(t, func() bool {
		tools := liveOf(t, opened, root).Tools
		return len(tools) > 0 && tools[0].Status == harness.ToolSlotSleeping
	})
	wake := settledWake(opened)
	parentSlot := liveOf(t, opened, root).Tools[0]
	script.add(fmt.Sprintf("parent call: %s %s", parentSlot.Name, parentSlot.Status))
	script.add(fmt.Sprintf("the person is asked: %q", questions))
	script.add("wake while everything waits for the person: " + wakeText(wake))

	// The person answers through whatever showed the question; the host delivers it.
	script.add("answer: " + string(must(harness.Signal(background, opened, questionIds[0], answerSignal, "answer", answerValue{Text: "Yes, after 5 pm"}))))
	settled := waitSubmission(t, submission)
	script.add("parent: " + string(settled.Status))
	script.add("subagent result: " + newestToolResult(t, root, 10))

	expectEqual(t, "script", script.all(), []string{
		"parent call: subagent sleeping",
		`the person is asked: ["May we deploy today?"]`,
		`wake while everything waits for the person: {"active":false}`,
		"answer: delivered",
		"parent: done",
		"subagent result: The person says yes, after 5 pm.",
	})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
