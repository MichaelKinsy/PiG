// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The Go port of harness-lifecycle.test.ts "Harness close" through driver.OpenHarness over the real core (the in-process port is
// durable/harness/harness_lifecycle_test.go). The native store is a SQLite file the owner goroutine holds, so "Storage is still
// open" becomes: Close does not return while the invocation runs, and the same file opens again once it has.

type gate struct{ reached, release chan struct{} }

func newGate() *gate { return &gate{reached: make(chan struct{}), release: make(chan struct{})} }

// stubborn is the invocation body: it announces itself and ignores its context until the test releases it.
func (g *gate) stubborn() {
	close(g.reached)
	<-g.release
}

func (g *gate) awaitReached(t *testing.T, what string) {
	t.Helper()
	select {
	case <-g.reached:
	case <-time.After(15 * time.Second):
		t.Fatalf("%s never started", what)
	}
}

// closeJoins asserts that Close waits for the gated invocation and then succeeds, and that the file opens again.
func closeJoins(t *testing.T, h harness.Harness, g *gate, path string, options harness.HarnessOptions) {
	t.Helper()
	closing := make(chan error, 1)
	go func() { closing <- h.Close(context.Background()) }()
	select {
	case err := <-closing:
		t.Fatalf("close returned while the invocation ignored its signal: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(g.release)
	select {
	case err := <-closing:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("close never returned after the invocation did")
	}
	again, err := driver.OpenHarness(t.Context(), path, options)
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	if err := again.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

type stepState struct {
	Phase string `json:"phase"`
}

// harness-lifecycle.test.ts:127 "joins a task handler that ignores its signal before closing Storage".
func TestCoreHarnessCloseJoinsATaskHandlerThatIgnoresItsSignal(t *testing.T) {
	g := newGate()
	stubborn := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name: "test.close-stubborn", Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue], runtime durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, any]) error {
				g.stubborn()
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					var result durable.JsonValue
					return &durable.NextTaskState[stepState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &result}}, nil
				})
			},
		},
		Abort: func(context.Context, durable.RunningTask[durable.JsonValue, stepState, durable.JsonValue], durable.TaskRuntime[durable.JsonValue, stepState, durable.JsonValue, any]) error {
			return nil
		},
	})
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{stubborn.Erased()}}); err != nil {
		t.Fatal(err)
	}
	options := harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry}
	path := filepath.Join(t.TempDir(), "store.db")
	h, err := driver.OpenHarness(t.Context(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	root, err := h.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, stubborn, nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
	}); err != nil {
		t.Fatal(err)
	}
	h.Resume()
	g.awaitReached(t, "the task handler")
	closeJoins(t, h, g, path, options)
}

// harness-lifecycle.test.ts:150 "joins a tool execute and a hook that ignore their signal before closing Storage".
func TestCoreHarnessCloseJoinsAToolAndAHookThatIgnoreTheirSignal(t *testing.T) {
	for _, where := range []string{"tool", "hook"} {
		t.Run(where, func(t *testing.T) {
			g := newGate()
			faux := ai.NewFauxProvider(ai.FauxConfig{})
			models := ai.CreateModels()
			models.SetProvider(faux.Provider())
			registry := harness.CreateRegistry()
			if err := registry.Install(&durable.Extension{Name: "tool:wait", Tools: []*durable.ToolRegistration{{
				ToolSchema: ai.ToolSchema{Name: "wait", Description: "wait", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
				Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					if where == "tool" {
						g.stubborn()
					}
					return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
				},
			}}}); err != nil {
				t.Fatal(err)
			}
			if err := registry.Install(&durable.Extension{Name: "hooks:1", Hooks: []durable.HookRegistration{{
				Task: harness.GenerationTask.AnyDefinition().Name,
				Handlers: &harness.GenerationHooks{BeforeRequest: func(context.Context, harness.GenerationRequest, harness.HookApi) (*harness.GenerationRequest, error) {
					if where == "hook" {
						g.stubborn()
					}
					return nil, nil
				}},
			}}}); err != nil {
				t.Fatal(err)
			}
			faux.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("wait", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
			})
			options := harness.HarnessOptions{Models: models, Registry: registry}
			path := filepath.Join(t.TempDir(), "store.db")
			h, err := driver.OpenHarness(t.Context(), path, options)
			if err != nil {
				t.Fatal(err)
			}
			root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")}); err != nil {
				t.Fatal(err)
			}
			g.awaitReached(t, where)
			closeJoins(t, h, g, path, options)
		})
	}
}
