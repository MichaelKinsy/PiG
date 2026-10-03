// Ports packages/durable/test/examples/11-extension-state.ts, 12-tasks.ts and 13-recovery.ts.

package examples_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

type todos struct {
	Items []string `json:"items"`
}

// 11-extension-state.ts: an extension keeps its own document, written by its tool and rendered into the prompt.
func TestExample11ExtensionState(t *testing.T) {
	todosDoc := durable.DefineDoc(durable.DocDefinition[todos]{
		CommonDocDefinition: durable.CommonDocDefinition[todos]{Kind: "example.todos", Version: 1},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: durable.ForkAsOf},
		Initial:             func() todos { return todos{Items: []string{}} },
	})
	todoTool := &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{
			Name:        "todo",
			Description: "Add an item to your todo list",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"item": map[string]any{"type": "string"}},
				"required":   []any{"item"},
			},
		},
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			item, _ := args.(map[string]any)["item"].(string)
			if _, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				draft, err := durable.TxDoc[todos](tx, todosDoc, api.ConversationId())
				if err != nil {
					return nil, err
				}
				_, err = draft.Array("items").Push(item)
				return nil, err
			}); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "added " + item}}}, nil
		},
	}
	todoExtension := new(durable.Extension{
		Name:  "todo",
		Tools: []*durable.ToolRegistration{todoTool},
		Sections: []*durable.PromptSection{{Key: "todos", Render: func(ctx context.Context, input durable.PromptInput) (*string, error) {
			current, err := durable.Snapshot[todos](ctx, input.Read, todosDoc, input.ConversationId)
			if err != nil || current == nil || len(current.Items) == 0 {
				return nil, err
			}
			text := strings.Join(current.Items, "\n")
			return &text, nil
		}}},
	})

	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	registry := harness.CreateRegistry()
	installed(t, registry, todoExtension)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	root, err := opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}

	answer := func(text string) ai.FauxResponseStep {
		return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
	}
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("todo", map[string]any{"item": "fix the build"}, "t1")}, StopReason: "toolUse"}),
		answer("Noted."),
		answer("Working on it."),
	})
	say := func(text string) {
		t.Helper()
		submission, err := root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := submission.Wait(background); err != nil {
			t.Fatal(err)
		}
	}
	say("Remember to fix the build.")
	current, err := durable.Snapshot[todos](background, opened, todosDoc, root.Id())
	if err != nil || current == nil {
		t.Fatalf("todos: %+v %v", current, err)
	}
	expectEqual(t, "todos", current.Items, []string{"fix the build"})

	say("What is next?")
	view := must(root.Context(background))
	var sections []map[string]string
	for _, message := range view.Messages {
		system, isSystem := message.(ai.SystemMessage)
		if !isSystem {
			continue
		}
		rendered := map[string]string{}
		for _, section := range system.Sections {
			if section.Value != nil {
				rendered[section.Name] = *section.Value
			}
		}
		sections = append(sections, rendered)
	}
	found := false
	for _, rendered := range sections {
		if rendered["todos"] == "<todos>\nfix the build\n</todos>" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no system message rendered the todos section: %v", sections)
	}
	closeSession(t, opened)
}

type paymentState struct {
	Phase string `json:"phase"`
	Key   string `json:"key,omitempty"`
}

type paymentInput struct {
	Amount int `json:"amount"`
}

type receipt struct {
	Receipt int `json:"receipt"`
}

// 12-tasks.ts: a durable state machine. The charge is idempotent: its effect is keyed, so a rerun cannot charge twice.
func TestExample12Tasks(t *testing.T) {
	payments := map[string]int{}
	type paymentTask = durable.TaskRuntime[paymentInput, paymentState, receipt, any]
	payment := durable.DefineTask(durable.TaskDefinition[paymentInput, paymentState, receipt, any]{
		Name:    "example.payment",
		Version: 1,
		Initial: func(paymentInput) paymentState { return paymentState{Phase: "prepare"} },
		Phases: map[string]durable.PhaseHandler[paymentInput, paymentState, receipt, any]{
			"prepare": func(ctx context.Context, task durable.RunningTask[paymentInput, paymentState, receipt], runtime paymentTask) error {
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[paymentInput, paymentState, receipt]) (*durable.NextTaskState[paymentState, receipt], error) {
					return &durable.NextTaskState[paymentState, receipt]{Status: durable.TaskRunning, Checkpoint: &paymentState{Phase: "charge", Key: fmt.Sprintf("payment-%d", task.Id)}}, nil
				})
			},
			"charge": func(ctx context.Context, task durable.RunningTask[paymentInput, paymentState, receipt], runtime paymentTask) error {
				key := task.State.Checkpoint.Key
				if _, charged := payments[key]; !charged {
					payments[key] = task.Input.Amount * 100
				}
				result := receipt{Receipt: payments[key]}
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[paymentInput, paymentState, receipt]) (*durable.NextTaskState[paymentState, receipt], error) {
					return &durable.NextTaskState[paymentState, receipt]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[receipt]{Status: durable.OutcomeCompleted, Result: &result}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[paymentInput, paymentState, receipt], runtime paymentTask) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[paymentInput, paymentState, receipt]) (*durable.NextTaskState[paymentState, receipt], error) {
				return &durable.NextTaskState[paymentState, receipt]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[receipt]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
	registry := harness.CreateRegistry()
	installed(t, registry, new(durable.Extension{Name: "payments", Tasks: []durable.AnyTask{payment}}))
	opened := openHarness(t, registry, nil)
	root := must(opened.Root(background, nil))
	paymentId := commit(t, root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, payment, paymentInput{Amount: 5}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
	})
	paid, err := opened.WaitForTask(background, paymentId)
	if err != nil {
		t.Fatal(err)
	}
	if paid.State.Outcome == nil || paid.State.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("payment outcome: %+v", paid.State.Outcome)
	}
	decoded, err := durable.FromJsonValue[receipt](*paid.State.Outcome.Result)
	if err != nil || decoded.Receipt != 500 {
		t.Fatalf("receipt: %+v %v", decoded, err)
	}
	closeSession(t, opened)
}

type tickState struct {
	Phase string `json:"phase"`
	N     int    `json:"n"`
}

// 13-recovery.ts: a task survives a close and a reopen of its storage. A memo keeps the visible effect (the print)
// from repeating when a phase reruns.
func TestExample13Recovery(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "session.sqlite")
	var printed []int
	var reachedTick atomic.Int64
	type tickInput = struct {
		To int `json:"to"`
	}
	ticker := durable.DefineTask(durable.TaskDefinition[tickInput, tickState, string, any]{
		Name:    "example.ticker",
		Version: 1,
		Initial: func(tickInput) tickState { return tickState{Phase: "tick", N: 1} },
		Phases: map[string]durable.PhaseHandler[tickInput, tickState, string, any]{
			"tick": func(ctx context.Context, task durable.RunningTask[tickInput, tickState, string], runtime durable.TaskRuntime[tickInput, tickState, string, any]) error {
				n := task.State.Checkpoint.N
				name := fmt.Sprintf("printed-%d", n)
				if _, present, err := runtime.Memo(ctx, name); err != nil {
					return err
				} else if !present {
					if _, err := runtime.MemoCandidate(ctx, name, true); err != nil {
						return err
					}
					printed = append(printed, n) // console.log(`tick ${n}`)
				}
				reachedTick.Store(int64(n))
				if err := runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[tickInput, tickState, string]) (*durable.NextTaskState[tickState, string], error) {
					if n == task.Input.To {
						result := fmt.Sprintf("counted to %d", n)
						return &durable.NextTaskState[tickState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: &result}}, nil
					}
					return &durable.NextTaskState[tickState, string]{Status: durable.TaskRunning, Checkpoint: &tickState{Phase: "tick", N: n + 1}}, nil
				}); err != nil {
					return err
				}
				return runtime.Sleep(ctx, float64(time.Now().UnixMilli()+50))
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[tickInput, tickState, string], runtime durable.TaskRuntime[tickInput, tickState, string, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[tickInput, tickState, string]) (*durable.NextTaskState[tickState, string], error) {
				return &durable.NextTaskState[tickState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
	registry := harness.CreateRegistry()
	installed(t, registry, new(durable.Extension{Name: "ticker", Tasks: []durable.AnyTask{ticker}}))
	open := func() harness.Harness {
		t.Helper()
		store, err := sqlitenode.OpenNodeSqliteStorage(databasePath, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry})
		if err != nil {
			t.Fatal(err)
		}
		return opened
	}

	firstRun := open()
	tickerId := commit(t, must(firstRun.Root(background, nil)), func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, ticker, tickInput{To: 5}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
	})
	firstRun.Resume()
	eventually(t, func() bool { return reachedTick.Load() >= 2 })
	closeSession(t, firstRun)

	reader := open()
	saved, err := reader.GetTask(background, tickerId)
	if err != nil || saved == nil {
		t.Fatalf("saved task: %v %v", saved, err)
	}
	closeSession(t, reader)
	if saved.State.Status == durable.TaskTerminal {
		t.Fatalf("the task finished before the close: %+v", saved.State)
	}

	secondRun := open()
	counted, err := secondRun.WaitForTask(background, tickerId)
	if err != nil {
		t.Fatal(err)
	}
	if counted.State.Outcome == nil || counted.State.Outcome.Status != durable.OutcomeCompleted || counted.State.Outcome.Result == nil || (*counted.State.Outcome.Result) != "counted to 5" {
		t.Fatalf("after reopen: %+v", counted.State.Outcome)
	}
	// Each tick printed once across the restart: the memo made the print survive a rerun of a phase.
	expectEqual(t, "printed ticks", printed, []int{1, 2, 3, 4, 5})
	closeSession(t, secondRun)
}
