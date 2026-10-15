// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// hookLog records which registered hooks ran, in call order.
type hookLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *hookLog) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
}

func (l *hookLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func echoToolSchema() ai.ToolSchema {
	return ai.ToolSchema{Name: "echo", Description: "echo", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}

// openHookHarness opens a native Harness whose registry holds the tool echo and the hooks of the generation and tool tasks.
func openHookHarness(t *testing.T, log *hookLog, toolHooks *harness.ToolHooks) durable.Submission {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{
		Name: "echo",
		Tools: []*durable.ToolRegistration{{ToolSchema: echoToolSchema(), Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			log.add("execute")
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
		}}},
		Hooks: []durable.HookRegistration{
			{Task: harness.GenerationTask.AnyDefinition().Name, Handlers: &harness.GenerationHooks{
				BeforeRequest: func(context.Context, harness.GenerationRequest, harness.HookApi) (*harness.GenerationRequest, error) {
					log.add("beforeRequest")
					return nil, nil
				},
				AfterResponse: func(context.Context, ai.AssistantMessage, harness.HookApi) error {
					log.add("afterResponse")
					return nil
				},
				AfterTools: func(context.Context, durable.EntryId, []durable.EntryId, harness.HookApi) error {
					log.add("afterTools")
					return nil
				},
				OnYield: func(context.Context, ai.AssistantMessage, harness.HookApi) (*harness.YieldContinue, error) {
					log.add("onYield")
					return nil, nil
				},
			}},
			{Task: harness.ToolTask.AnyDefinition().Name, Handlers: toolHooks},
		},
	}); err != nil {
		t.Fatal(err)
	}
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("echo", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	h, err := driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func waitHooks(t *testing.T, log *hookLog, sub durable.Submission) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if _, err := sub.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	return log.all()
}

// The hooks a registry holds reach a native Harness: the agent the host resolves lists them with their handler indexes, so the
// core asks the host for each one (agent.ts agentHooks; the JS host's agentJSON). Before the fix agentJSON omitted them and no
// registered hook ever ran natively.
func TestCoreHarnessRunsTheRegisteredGenerationAndToolHooks(t *testing.T) {
	log := &hookLog{}
	toolHooks := &harness.ToolHooks{
		BeforeTool: func(_ context.Context, call harness.ToolHookCall, _ harness.HookApi) (*harness.BeforeToolResult, error) {
			log.add("beforeTool:" + call.Name)
			return nil, nil
		},
		AfterTool: func(_ context.Context, call harness.ToolHookCall, _ durable.ToolExecutionResult, _ harness.HookApi) (*durable.ToolExecutionResult, error) {
			log.add("afterTool:" + call.Name)
			return nil, nil
		},
	}
	got := waitHooks(t, log, openHookHarness(t, log, toolHooks))
	want := []string{"beforeRequest", "afterResponse", "beforeTool:echo", "execute", "afterTool:echo", "afterTools", "beforeRequest", "afterResponse", "onYield"}
	if len(got) != len(want) {
		t.Fatalf("hooks = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hooks = %v, want %v", got, want)
		}
	}
}

// Pi types.ts:740 ToolHookCall = ToolCall & {parent?}: both tool hooks of a native Harness read the call's type "toolCall"
// (ai types.ts:437) and, for a model-issued call, no parent (tool.ts:208-221 readCall returns the assistant entry's call).
func TestCoreHarnessToolHooksSeeTheToolCallType(t *testing.T) {
	log := &hookLog{}
	see := func(hook string, call harness.ToolHookCall) {
		parent := "none"
		if call.Parent != nil {
			parent = "set"
		}
		log.add(hook + ":" + call.Type + ":" + call.ID + ":" + parent)
	}
	toolHooks := &harness.ToolHooks{
		BeforeTool: func(_ context.Context, call harness.ToolHookCall, _ harness.HookApi) (*harness.BeforeToolResult, error) {
			see("beforeTool", call)
			return nil, nil
		},
		AfterTool: func(_ context.Context, call harness.ToolHookCall, _ durable.ToolExecutionResult, _ harness.HookApi) (*durable.ToolExecutionResult, error) {
			see("afterTool", call)
			return nil, nil
		},
	}
	got := waitHooks(t, log, openHookHarness(t, log, toolHooks))
	for _, want := range []string{"beforeTool:toolCall:c1:none", "afterTool:toolCall:c1:none"} {
		if !contains(got, want) {
			t.Fatalf("hooks = %v, want %q", got, want)
		}
	}
}

// A beforeTool hook that blocks the call keeps the tool from running.
func TestCoreHarnessBeforeToolHookBlocksTheCall(t *testing.T) {
	log := &hookLog{}
	reason := "not today"
	toolHooks := &harness.ToolHooks{
		BeforeTool: func(_ context.Context, call harness.ToolHookCall, _ harness.HookApi) (*harness.BeforeToolResult, error) {
			log.add("beforeTool:" + call.Name)
			return &harness.BeforeToolResult{Block: &reason}, nil
		},
	}
	got := waitHooks(t, log, openHookHarness(t, log, toolHooks))
	for _, line := range got {
		if line == "execute" {
			t.Fatalf("a blocked call must not execute: %v", got)
		}
	}
	if len(got) == 0 || got[0] != "beforeRequest" || !contains(got, "beforeTool:echo") {
		t.Fatalf("hooks = %v", got)
	}
}

func contains(lines []string, line string) bool {
	return slices.Contains(lines, line)
}
