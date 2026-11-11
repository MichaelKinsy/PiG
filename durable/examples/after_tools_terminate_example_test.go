// Ports packages/durable/test/examples/46-after-tools-terminate.ts: an `afterTools` hook that asks a person, after every
// tool round, whether the agent should go on. Its task parks on the person's answer, so the Session needs no process
// meanwhile; "go on" answers the run with a steer the next request sees, "stop" ends the run with `{ terminate: true }`,
// as a round whose calls all asked to terminate. The script's printing is the transcript this test reads.
//
// Go mapping: the script's pauses of a few milliseconds become waits for the asking task to sleep and settledWake; the
// second question is the task that was not answered (a pause cannot tell a finished task's entry from the next one).

package examples_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/schema"
)

type choiceValue struct {
	Choice string `json:"choice"`
}

type goOnState struct {
	Phase string `json:"phase"`
}

// Pi: packages/durable/src/harness/generation.ts (afterTools tasks, terminate).
func TestExample46AfterToolsTerminate(t *testing.T) {
	choiceSignal := durable.DefineSignal(durable.SignalDefinition[choiceValue]{
		Name: "example.go-on", Version: 1, Schema: schema.Obj(schema.Properties{schema.Prop("choice", schema.OneOf([]any{"go on", "stop"}))}),
	})
	var askingMu sync.Mutex
	asking := map[durable.TaskId]bool{}

	// Asks, sleeps until the person answers, then steers or ends the run.
	type goOnRuntime = durable.TaskRuntime[any, goOnState, *harness.AfterToolsDecision, any]
	type goOnTask = durable.RunningTask[any, goOnState, *harness.AfterToolsDecision]
	type goOnNext = durable.NextTaskState[goOnState, *harness.AfterToolsDecision]
	askToGoOn := durable.DefineTask(durable.TaskDefinition[any, goOnState, *harness.AfterToolsDecision, any]{
		Name:    "example.ask-to-go-on",
		Version: 1,
		Initial: func(any) goOnState { return goOnState{Phase: "ask"} },
		Phases: map[string]durable.PhaseHandler[any, goOnState, *harness.AfterToolsDecision, any]{
			"ask": func(ctx context.Context, _ goOnTask, runtime goOnRuntime) error {
				askingMu.Lock()
				asking[runtime.TaskId()] = true
				askingMu.Unlock()
				return runtime.Commit(ctx, func(durable.Tx, goOnTask) (*goOnNext, error) {
					return &goOnNext{Status: durable.TaskSleeping, Checkpoint: &goOnState{Phase: "answered"}, Signals: []durable.SignalKey{choiceSignal.Key("choice")}}, nil
				})
			},
			"answered": func(ctx context.Context, _ goOnTask, runtime goOnRuntime) error {
				askingMu.Lock()
				delete(asking, runtime.TaskId())
				askingMu.Unlock()
				state, err := durable.ReadSignal(ctx, runtime, choiceSignal, "choice")
				if err != nil {
					return err
				}
				choice := "stop"
				if state != nil && !state.Expired {
					choice = state.Value.Choice
				}
				// The handle is fetched before the commit: inside it, runtime reads would wait for the commit itself.
				handle, err := runtime.Conversation(ctx, runtime.ConversationId())
				if err != nil {
					return err
				}
				return runtime.Commit(ctx, func(tx durable.Tx, _ goOnTask) (*goOnNext, error) {
					if choice == "stop" {
						terminate := &harness.AfterToolsDecision{Terminate: true}
						return &goOnNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[*harness.AfterToolsDecision]{Status: durable.OutcomeCompleted, Result: &terminate}}, nil
					}
					// A steer in the task's terminal commit: the round's boundary places it before the next request.
					if _, err := handle.SubmitIn(tx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("The person says: go on."), WhenBusy: durable.WhenBusySteer}); err != nil {
						return nil, err
					}
					var none *harness.AfterToolsDecision
					return &goOnNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[*harness.AfterToolsDecision]{Status: durable.OutcomeCompleted, Result: &none}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ goOnTask, runtime goOnRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, goOnTask) (*goOnNext, error) {
				return &goOnNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[*harness.AfterToolsDecision]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})

	step := &durable.ToolRegistration{
		Name:        "step",
		Description: "Do one step of the work",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"what": map[string]any{"type": "string"}},
			"required":   []any{"what"},
		},
		Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return textResult("did " + args.(map[string]any)["what"].(string)), nil
		},
	}

	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	callStep := func(what, id string) ai.FauxResponseStep {
		return fauxToolTurn("step", map[string]any{"what": what}, id)
	}
	faux.SetResponses([]ai.FauxResponseStep{callStep("step 1", "s1"), callStep("step 2", "s2"), fauxAnswer("Not reached.")})
	registry := harness.CreateRegistry()
	installed(t, registry, &durable.Extension{
		Name:  "checkpoints",
		Tools: []*durable.ToolRegistration{step},
		Tasks: []durable.AnyTask{askToGoOn.Erased()},
		Hooks: []durable.HookRegistration{harness.Hook(harness.GenerationTask, &harness.GenerationHooks{
			AfterTools: func(context.Context, durable.EntryId, []durable.EntryId, harness.HookApi) (*harness.HookTasks, error) {
				return &harness.HookTasks{Tasks: []harness.HookTask{harness.HookTaskOf[any](askToGoOn, nil)}}, nil
			},
		})},
	})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry, OnWake: func(harness.Wake) {}})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	// asked returns the task that asks the person now, other than `answered`, once it sleeps.
	asked := func(answered durable.TaskId) durable.TaskId {
		t.Helper()
		var task durable.TaskId
		eventually(t, func() bool {
			askingMu.Lock()
			defer askingMu.Unlock()
			for id := range asking {
				if id != answered {
					task = id
					return true
				}
			}
			return false
		})
		waitSleeping(t, opened, task)
		return task
	}

	var script printed
	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Do the work, step by step")}))
	first := asked(0)
	script.add("round 1 done, asking the person; wake: " + wakeText(settledWake(opened)))
	script.add("answer: " + string(must(harness.Signal(background, opened, first, choiceSignal, "choice", choiceValue{Choice: "go on"}))))
	second := asked(first)
	script.add("round 2 done, asking again; wake: " + wakeText(settledWake(opened)))
	script.add("answer: " + string(must(harness.Signal(background, opened, second, choiceSignal, "choice", choiceValue{Choice: "stop"}))))
	settled := waitSubmission(t, submission)
	script.add(fmt.Sprintf("run: %s with requests: %d", settled.Status, faux.CallCount()))
	ascending := durable.ScanAscending
	page := must(root.Entries(background, durable.EntryQuery{Order: &ascending}, 50, nil))
	for _, entry := range page.Items {
		if len(entry.Model) == 0 {
			continue
		}
		switch message := entry.Model[0].(type) {
		case ai.UserMessage:
			text, _ := message.Content.(ai.UserText)
			script.add("> " + string(text))
		case ai.ToolResultMessage:
			script.add("  tool: " + partsText(message.Content))
		}
	}

	expectEqual(t, "script", script.all(), []string{
		`round 1 done, asking the person; wake: {"active":false}`,
		"answer: delivered",
		`round 2 done, asking again; wake: {"active":false}`,
		"answer: delivered",
		"run: done with requests: 2",
		"> Do the work, step by step",
		"  tool: did step 1",
		"> The person says: go on.",
		"  tool: did step 2",
	})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
