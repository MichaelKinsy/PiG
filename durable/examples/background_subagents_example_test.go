// Ports packages/durable/test/examples/23-subagent-background.ts: persistent background subagents. One tool starts
// named subagents, messages them, stops them mid-answer and lists them; each answer is reported back to the main
// agent as a new message once it arrives; everything survives a restart. The script's colored printing of the main
// conversation is the transcript this test reads.

package examples_test

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

type subagentRecord struct {
	ConversationId durable.ConversationId `json:"conversationId"`
	// Reported are the answers already reported to the main agent: several messages can end in one answer,
	// reported once.
	Reported []durable.EntryId `json:"reported"`
}

type subagentsState struct {
	Agents    map[string]subagentRecord `json:"agents"`
	Reporters map[string]durable.TaskId `json:"reporters"`
}

type reporterInput struct {
	Name           string                 `json:"name"`
	ConversationId durable.ConversationId `json:"conversationId"`
	Message        string                 `json:"message"`
	FollowUp       bool                   `json:"followUp"`
}

type reporterState struct {
	Phase  string  `json:"phase"`
	Report *string `json:"report,omitempty"`
}

type anchorState struct {
	Phase string `json:"phase"`
}

// Pi: packages/durable/src/harness/types.ts:124 (abort).
func TestExample23SubagentBackground(t *testing.T) {
	// Each subagent is its own conversation. The main conversation keeps a small document that maps subagent names
	// to their conversations. A fork of the main conversation starts without subagents.
	subagentsDoc := durable.DefineDoc(durable.DocDefinition[subagentsState]{
		CommonDocDefinition: durable.CommonDocDefinition[subagentsState]{Kind: "app.subagents", Version: 1, Initial: func() subagentsState {
			return subagentsState{Agents: map[string]subagentRecord{}, Reporters: map[string]durable.TaskId{}}
		}},
		DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
	})

	// A subagent must outlive the main agent's turns, so its conversation is owned by an anchor: a background task
	// that finishes at once.
	type anchorRuntime = durable.TaskRuntime[durable.JsonValue, anchorState, durable.JsonValue, any]
	type anchorRunning = durable.RunningTask[durable.JsonValue, anchorState, durable.JsonValue]
	settle := func(ctx context.Context, runtime anchorRuntime, outcome durable.TaskOutcome[durable.JsonValue]) error {
		return runtime.Commit(ctx, func(durable.Tx, anchorRunning) (*durable.NextTaskState[anchorState, durable.JsonValue], error) {
			return &durable.NextTaskState[anchorState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &outcome}, nil
		})
	}
	anchor := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, anchorState, durable.JsonValue, any]{
		Name:    "app.subagent-anchor",
		Version: 1,
		Initial: func(durable.JsonValue) anchorState { return anchorState{Phase: "done"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, anchorState, durable.JsonValue, any]{
			"done": func(ctx context.Context, _ anchorRunning, runtime anchorRuntime) error {
				return settle(ctx, runtime, durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted})
			},
		},
		Abort: func(ctx context.Context, _ anchorRunning, runtime anchorRuntime) error {
			return settle(ctx, runtime, durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted})
		},
	})

	// A reporter delivers one message to a subagent and reports the answer to the main agent. Request IDs make a
	// repeated submission return the first one, so the subagent gets the message once and the main agent gets the
	// report once.
	type reporterRuntime = durable.TaskRuntime[reporterInput, reporterState, durable.JsonValue, any]
	type reporterRunning = durable.RunningTask[reporterInput, reporterState, durable.JsonValue]
	type reporterNext = durable.NextTaskState[reporterState, durable.JsonValue]
	reporter := durable.DefineTask(durable.TaskDefinition[reporterInput, reporterState, durable.JsonValue, any]{
		Name:    "app.subagent-reporter",
		Version: 1,
		Initial: func(reporterInput) reporterState { return reporterState{Phase: "deliver"} },
		Phases: map[string]durable.PhaseHandler[reporterInput, reporterState, durable.JsonValue, any]{
			// Send the message, wait for its answer, and decide what to report.
			"deliver": func(ctx context.Context, task reporterRunning, runtime reporterRuntime) error {
				input := task.Input
				// While the subagent is busy, a steer reaches it at its next step and a follow-up after its current
				// answer.
				subagent, err := runtime.Conversation(ctx, input.ConversationId)
				if err != nil || subagent == nil {
					return fmt.Errorf("subagent conversation: %w", err)
				}
				whenBusy := durable.WhenBusySteer
				if input.FollowUp {
					whenBusy = durable.WhenBusyFollowUp
				}
				submission, err := subagent.Submit(ctx, durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(input.Message), WhenBusy: whenBusy, RequestId: new(fmt.Sprintf("subagent:%d", task.Id))})
				if err != nil {
					return err
				}
				settled, err := submission.Wait(ctx)
				if err != nil {
					return err
				}
				// One commit decides the report and records the answer as delivered, so a restart does not decide
				// again.
				return runtime.Commit(ctx, func(tx durable.Tx, _ reporterRunning) (*reporterNext, error) {
					next := func(report *string) (*reporterNext, error) {
						return &reporterNext{Status: durable.TaskRunning, Checkpoint: &reporterState{Phase: "report", Report: report}}, nil
					}
					// aborted: stopped, or withdrawn while queued. Nothing to report.
					if settled.Status == durable.SubmissionUnanswered {
						if settled.Reason != nil && *settled.Reason == "aborted" {
							return next(nil)
						}
						reason := ""
						if settled.Reason != nil {
							reason = *settled.Reason
						}
						return next(new(fmt.Sprintf("[subagent %s failed: %s]", input.Name, reason)))
					}
					if settled.Type != durable.SubmissionTypeInput || settled.Answer == nil {
						return next(nil)
					}
					draft, err := durable.TxDoc[subagentsState](tx, subagentsDoc, runtime.ConversationId())
					if err != nil {
						return nil, err
					}
					agent := draft.Object("agents").Object(input.Name)
					reported, err := durable.FromJsonValue[[]durable.EntryId](agent.Array("reported").Snapshot())
					if err != nil {
						return nil, err
					}
					if slices.Contains(reported, *settled.Answer) {
						return next(nil)
					}
					if _, err := agent.Array("reported").Push(float64(*settled.Answer)); err != nil {
						return nil, err
					}
					entry, err := tx.Entry(*settled.Answer)
					if err != nil || entry == nil {
						return nil, fmt.Errorf("answer entry: %w", err)
					}
					return next(new(fmt.Sprintf("[subagent %s answered, no reply needed] %s", input.Name, assistantText(entry.Model[0].(ai.AssistantMessage)))))
				})
			},
			// Post the report as a follow-up input: it starts a turn when the main agent is idle, or waits for its
			// current answer.
			"report": func(ctx context.Context, task reporterRunning, runtime reporterRuntime) error {
				if report := task.State.Checkpoint.Report; report != nil {
					main, err := runtime.Conversation(ctx, runtime.ConversationId())
					if err != nil || main == nil {
						return fmt.Errorf("main conversation: %w", err)
					}
					if _, err := main.Submit(ctx, durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(*report), WhenBusy: durable.WhenBusyFollowUp, RequestId: new(fmt.Sprintf("subagent-report:%d", task.Id))}); err != nil {
						return err
					}
				}
				return runtime.Commit(ctx, func(durable.Tx, reporterRunning) (*reporterNext, error) {
					return &reporterNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ reporterRunning, runtime reporterRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, reporterRunning) (*reporterNext, error) {
				return &reporterNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})

	var subagentTools *durable.Extension
	subagentTool := new(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{
			Name: "subagent",
			Description: "Manage persistent subagents that work in the background. Actions: spawn (name, message), send (name, message; " +
				"followUp: true queues it after the current answer instead of steering), stop (name: aborts its current work), " +
				"status (name, or all subagents without one). Answers are reported back to you when they arrive.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":   map[string]any{"type": "string", "enum": []any{"spawn", "send", "stop", "status"}},
					"name":     map[string]any{"type": "string"},
					"message":  map[string]any{"type": "string"},
					"followUp": map[string]any{"type": "boolean"},
				},
				"required": []any{"action"},
			},
		},
		// A call interrupted by a crash is not rerun: repeating stop could stop newer work. The model sees that the
		// call was interrupted and can check with status.
		Replay: durable.ReplayUnsafe,
		Execute: func(ctx context.Context, raw any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			args := raw.(map[string]any)
			action, _ := args["action"].(string)
			name, hasName := args["name"].(string)
			message, hasMessage := args["message"].(string)
			followUp, _ := args["followUp"].(bool)
			reply := func(text string, conversationId *durable.ConversationId) (durable.ToolExecutionResult, error) {
				result := textResult(text)
				if conversationId != nil && hasName {
					// A UI can attach to the subagent's conversation through the call's details.
					result.Details, result.HasDetails = delta.JsonObjectOf("name", name, "conversationId", float64(*conversationId)), true
				}
				return result, nil
			}
			registry := subagentsState{Agents: map[string]subagentRecord{}, Reporters: map[string]durable.TaskId{}}
			if current, err := durable.Snapshot[subagentsState](ctx, api, subagentsDoc, api.ConversationId()); err != nil {
				return durable.ToolExecutionResult{}, err
			} else if current != nil {
				registry = *current
			}

			if action == "status" {
				names := make([]string, 0, len(registry.Agents))
				for each := range registry.Agents {
					names = append(names, each)
				}
				slices.Sort(names)
				if hasName {
					names = []string{name}
				}
				var lines []string
				for _, each := range names {
					found, ok := registry.Agents[each]
					if !ok {
						continue
					}
					// A conversation is busy while it has a run: from an input until its final answer.
					live, err := durable.Snapshot[harness.LiveState](ctx, api, harness.LiveDoc, found.ConversationId)
					if err != nil {
						return durable.ToolExecutionResult{}, err
					}
					state := "idle"
					if live != nil && live.Run != nil {
						state = "working"
					}
					lines = append(lines, each+": "+state)
				}
				if len(lines) == 0 {
					return reply("No subagents.", nil)
				}
				return reply(strings.Join(lines, "\n"), nil)
			}
			if !hasName {
				return reply(action+" needs a name.", nil)
			}
			agent, exists := registry.Agents[name]
			if action != "spawn" && !exists {
				return reply("No subagent named "+name+".", nil)
			}
			if action == "stop" {
				// Aborts the subagent's current answer and tools and drops its queued messages. It stays usable.
				handle, err := api.Conversation(ctx, agent.ConversationId)
				if err != nil || handle == nil {
					return durable.ToolExecutionResult{}, fmt.Errorf("subagent conversation: %w", err)
				}
				if err := handle.Abort(ctx, nil); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return reply("Stopped "+name+".", &agent.ConversationId)
			}
			if !hasMessage {
				return reply(action+" needs a message.", nil)
			}

			// spawn and send: one commit starts a reporter for the message.
			committed, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				state, err := durable.TxDoc[subagentsState](tx, subagentsDoc, api.ConversationId())
				if err != nil {
					return nil, err
				}
				// Both tasks belong to the main conversation and are background: its Esc and idle waits skip them.
				background := durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}, Background: true}
				if action == "spawn" {
					if state.Object("agents").Has(name) {
						return name + " already exists; use send.", nil
					}
					anchorId, err := durable.CreateTask(tx, anchor, nil, background)
					if err != nil {
						return nil, err
					}
					// Owned by a task of the main conversation: starts as a copy of the main agent.
					child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: anchorId}})
					if err != nil {
						return nil, err
					}
					// Subagents cannot start subagents, and know who they are.
					if err := harness.Configure(tx, child.Id, harness.AgentChange{
						Extensions:   harness.SetTo(harness.ExtensionChange{Remove: []*durable.Extension{subagentTools}}),
						Instructions: harness.SetTo(fmt.Sprintf("You are the subagent %q. Answer the main agent's requests.", name)),
					}); err != nil {
						return nil, err
					}
					if err := state.Object("agents").Set(name, map[string]any{"conversationId": float64(child.Id), "reported": []any{}}); err != nil {
						return nil, err
					}
				}
				conversationId := durable.ConversationId(state.Object("agents").Object(name).Get("conversationId").(float64))
				reporterId, err := durable.CreateTask(tx, reporter, reporterInput{Name: name, ConversationId: conversationId, Message: message, FollowUp: action == "send" && followUp}, background)
				if err != nil {
					return nil, err
				}
				if err := state.Object("reporters").Set(strconv.FormatInt(int64(api.TaskId()), 10), float64(reporterId)); err != nil {
					return nil, err
				}
				if action == "send" {
					return "Sent to " + name + ".", nil
				}
				return "Started " + name + ".", nil
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			current, err := durable.Snapshot[subagentsState](ctx, api, subagentsDoc, api.ConversationId())
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			var conversationId *durable.ConversationId
			if current != nil {
				if found, ok := current.Agents[name]; ok {
					conversationId = &found.ConversationId
				}
			}
			return reply(committed.(string), conversationId)
		},
	})
	// The task definitions come with the extension, so pending reporters resume after a restart once the host
	// installs it again.
	subagentTools = new(durable.Extension{Name: "subagent-tools", Tasks: []durable.AnyTask{anchor, reporter}, Tools: []*durable.ToolRegistration{subagentTool}})

	// The main agent and the subagent share one scripted model, which answers each request by its last message. It
	// streams its answers at a pace that leaves the long walk-through stoppable.
	call := func(input map[string]any) ai.FauxResponse {
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("subagent", input, &ai.FauxToolCallOptions{ID: ""})}, StopReason: "toolUse"}
	}
	answer := func(text string) ai.FauxResponse {
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}}
	}
	route := func(transcript ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		// System messages carry prompt changes; the request is about the message before them.
		var last ai.Message
		for _, message := range slices.Backward(transcript.Messages()) {
			if _, system := message.(ai.SystemMessage); !system {
				last = message
				break
			}
		}
		var text strings.Builder
		switch message := last.(type) {
		case ai.UserMessage:
			switch content := message.Content.(type) {
			case ai.UserText:
				text.WriteString(string(content))
			case ai.UserContentBlocks:
				for _, part := range content {
					if part, ok := part.(ai.TextContent); ok {
						text.WriteString(part.Text)
					}
				}
			}
		case ai.ToolResultMessage:
			for _, part := range message.Content {
				if part, ok := part.(ai.TextContent); ok {
					text.WriteString(part.Text)
				}
			}
			// The main agent repeats what the tool said.
			return answer("OK. " + text.String()).AssistantMessage(), nil
		}
		said := text.String()
		switch {
		// The main agent.
		case strings.Contains(said, "Start a subagent"):
			return call(map[string]any{"action": "spawn", "name": "reader", "message": "Summarize the plot of Moby Dick."}).AssistantMessage(), nil
		case strings.Contains(said, "whale's name"):
			return call(map[string]any{"action": "send", "name": "reader", "message": "What is the whale called?"}).AssistantMessage(), nil
		case strings.Contains(said, "every chapter"):
			return call(map[string]any{"action": "send", "name": "reader", "message": "Now go through all chapters in detail."}).AssistantMessage(), nil
		case strings.Contains(said, "Stop reader"):
			return call(map[string]any{"action": "stop", "name": "reader"}).AssistantMessage(), nil
		case strings.Contains(said, "my subagents"):
			return call(map[string]any{"action": "status"}).AssistantMessage(), nil
		case strings.Contains(said, "[subagent"):
			return answer("Noted.").AssistantMessage(), nil
		// The subagent: short answers, and a long chapter walk-through that is stopped halfway.
		case strings.Contains(said, "Summarize the plot"):
			return answer("A whale, a captain, an obsession.").AssistantMessage(), nil
		case strings.Contains(said, "whale called"):
			return answer("Moby Dick.").AssistantMessage(), nil
		}
		chapters := make([]string, 135)
		for index := range chapters {
			chapters[index] = fmt.Sprintf("Chapter %d: more whaling.", index+1)
		}
		return answer(strings.Join(chapters, "\n")).AssistantMessage(), nil
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{TokensPerSecond: 500})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	steps := make([]ai.FauxResponseStep, 40)
	for i := range steps {
		steps[i] = ai.FauxFactoryStep(route)
	}
	faux.SetResponses(steps)

	registry := harness.CreateRegistry()
	installed(t, registry, subagentTools)
	databasePath := filepath.Join(t.TempDir(), "session.sqlite")
	open := func() (harness.Harness, harness.Conversation) {
		store, err := sqlitenode.OpenNodeSqliteStorage(databasePath, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{Models: models, Registry: registry})
		if err != nil {
			t.Fatal(err)
		}
		return opened, must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	}
	opened, root := open()
	snapshot := func() subagentsState {
		current := must(durable.Snapshot[subagentsState](background, opened, subagentsDoc, root.Id()))
		if current == nil {
			return subagentsState{}
		}
		return *current
	}
	// Wait until every message to a subagent was answered and reported, and the main agent has reacted.
	settleAll := func() {
		t.Helper()
		for _, id := range snapshot().Reporters {
			if _, err := opened.WaitForTask(background, id); err != nil {
				t.Fatal(err)
			}
		}
		if err := root.WaitForIdle(background); err != nil {
			t.Fatal(err)
		}
	}
	working := func(name string) bool {
		agent, ok := snapshot().Agents[name]
		if !ok {
			return false
		}
		live := must(durable.Snapshot[harness.LiveState](background, opened, harness.LiveDoc, agent.ConversationId))
		return live != nil && live.Run != nil
	}

	// The main agent answers at once; the subagent's answer is reported back when it arrives.
	say(t, root, "Start a subagent named reader that summarizes Moby Dick.")
	settleAll()

	// A long request, stopped while the subagent is still answering.
	say(t, root, "Ask reader to summarize every chapter.")
	eventually(t, func() bool { return working("reader") })
	say(t, root, "Stop reader.")
	settleAll()

	say(t, root, "What are my subagents doing?")
	settleAll()

	// The process stops while the subagent works on a message; after the restart its answer still arrives.
	before := len(snapshot().Reporters)
	must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Ask reader for the whale's name.")}))
	eventually(t, func() bool { return len(snapshot().Reporters) > before })
	closeSession(t, opened)
	opened, root = open()
	// After the restart the waits below enable scheduling; the reporter finishes, the report reaches the main agent
	// and the main agent reacts to it.
	settleAll()

	eventually(t, func() bool {
		page := must(root.Entries(background, durable.EntryQuery{}, 200, nil))
		return len(page.Items) > 0 && durable.AssistantEntry.Is(&page.Items[0]) && assistantText(page.Items[0].Model[0].(ai.AssistantMessage)) == "Noted."
	})

	// The main conversation as a user sees it: the user's messages and each subagent's report, in order.
	reportPattern := regexp.MustCompile(`^\[subagent (\S+) ([^\]]*)\] ?(.*)$`)
	var shown []string
	page := must(root.Entries(background, durable.EntryQuery{}, 200, nil))
	for _, entry := range slices.Backward(page.Items) {
		if len(entry.Model) == 0 {
			continue
		}
		switch message := entry.Model[0].(type) {
		case ai.UserMessage:
			text, _ := message.Content.(ai.UserText)
			if match := reportPattern.FindStringSubmatch(string(text)); match != nil {
				shown = append(shown, "report "+match[1]+": "+match[3])
			} else {
				shown = append(shown, "user: "+string(text))
			}
		case ai.AssistantMessage:
			if text := assistantText(message); text != "" {
				shown = append(shown, "assistant: "+text)
			}
		case ai.ToolResultMessage:
			var text strings.Builder
			for _, part := range message.Content {
				if part, ok := part.(ai.TextContent); ok {
					text.WriteString(part.Text)
				}
			}
			shown = append(shown, "result: "+text.String())
		}
	}
	// Each subagent answer is reported exactly once, the stopped chapter walk-through never.
	count := func(prefix string) (n int) {
		for _, line := range shown {
			if strings.HasPrefix(line, prefix) {
				n++
			}
		}
		return n
	}
	t.Logf("main conversation:\n%s", strings.Join(shown, "\n"))
	if got := count("report reader: A whale, a captain, an obsession."); got != 1 {
		t.Fatalf("plot reports = %d", got)
	}
	if got := count("report reader: Moby Dick."); got != 1 {
		t.Fatalf("whale-name reports after the restart = %d", got)
	}
	if got := count("report reader: Chapter"); got != 0 {
		t.Fatalf("the stopped walk-through was reported %d times", got)
	}
	for _, want := range []string{"result: Started reader.", "result: Stopped reader.", "result: reader: idle"} {
		if count(want) != 1 {
			t.Fatalf("%q appears %d times", want, count(want))
		}
	}
	closeSession(t, opened)
}
