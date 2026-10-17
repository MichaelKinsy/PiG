// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// harness-nested-tools-restart.test.ts:75, over the core: a replay-unsafe caller that never resumed after a restart has its
// unfinished nested calls abandoned before they run again. The one stopped in its execute phase settles `interrupted` (it may
// have partially run) and the one stopped before its intent `abandoned`, with the results the abort handler builds from the
// task's abort reason and checkpoint phase (tool.ts abort).
func TestCoreAbandonedNestedCallsAfterARestartSettleInterruptedOrAbandoned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	var mu sync.Mutex
	runs := map[string]int{}
	running, gated := make(chan struct{}), make(chan struct{})
	var runningOnce, gatedOnce sync.Once
	extension := &durable.Extension{
		Name: "nested",
		Tools: []*durable.ToolRegistration{
			func() *durable.ToolRegistration {
				registration := nestedCoreTool("started", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
					mu.Lock()
					runs["started"]++
					mu.Unlock()
					api.Output("partial\n")
					if err := api.Details(ctx, map[string]any{"step": 1.0}); err != nil {
						return durable.ToolExecutionResult{}, err
					}
					runningOnce.Do(func() { close(running) })
					<-ctx.Done()
					return durable.ToolExecutionResult{}, ctx.Err()
				})
				registration.Replay = durable.ReplaySafe
				return registration
			}(),
			nestedCoreTool("gated", func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				mu.Lock()
				runs["gated"]++
				mu.Unlock()
				return durable.ToolExecutionResult{}, nil
			}),
			nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				var wg sync.WaitGroup
				wg.Go(func() { _, _ = api.ExecuteTool(ctx, "started", map[string]any{}) })
				select {
				case <-running:
				case <-ctx.Done():
					return durable.ToolExecutionResult{}, ctx.Err()
				}
				wg.Go(func() { _, _ = api.ExecuteTool(ctx, "gated", map[string]any{}) })
				wg.Wait()
				return durable.ToolExecutionResult{}, nil
			}),
		},
		// Holds `gated` in its call phase, before its intent, until the Harness closes.
		Hooks: []durable.HookRegistration{{Task: harness.ToolTask.AnyDefinition().Name, Handlers: &harness.ToolHooks{
			BeforeTool: func(ctx context.Context, call harness.ToolHookCall, _ harness.HookApi) (*harness.BeforeToolResult, error) {
				if call.Name != "gated" {
					return nil, nil
				}
				gatedOnce.Do(func() { close(gated) })
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}}},
	}
	// One provider for both Harnesses, so the reopened one continues the script.
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("batch", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	open := func() (harness.Harness, harness.Conversation) {
		models := ai.CreateModels()
		models.SetProvider(faux.Provider())
		registry := harness.CreateRegistry()
		if err := registry.Install(extension); err != nil {
			t.Fatal(err)
		}
		h, err := driver.OpenHarness(t.Context(), path, harness.HarnessOptions{Models: models, Registry: registry})
		if err != nil {
			t.Fatal(err)
		}
		root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
		if err != nil {
			t.Fatal(err)
		}
		return h, root
	}
	h, root := open()
	h.Resume()
	sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("go")})
	if err != nil {
		t.Fatal(err)
	}
	<-running
	<-gated
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	h, root = open()
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	var endsMu sync.Mutex
	ends := map[string]*durable.NestedToolExecutionResult{}
	runEnded := false
	stream, err := harness.WatchEvents(t.Context(), h, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	stream.Start(func(_ context.Context, batch []harness.AgentEvent) error {
		endsMu.Lock()
		defer endsMu.Unlock()
		for _, event := range batch {
			if end, ok := event.(harness.ToolExecutionEndEvent); ok {
				ends[end.ToolCallId] = end.Result
			}
			if _, ok := event.(harness.RunEndEvent); ok {
				runEnded = true
			}
		}
		return nil
	})
	h.Resume()
	reopened, err := h.Submission(t.Context(), sub.Id())
	if err != nil {
		t.Fatal(err)
	}
	nestedCoreWait(t, reopened, durable.SubmissionDone)
	if err := h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The stream delivers on its own goroutine, so idle does not mean delivered: wait for the run's end before stopping,
	// which drops undelivered batches.
	pollUntil(t, "the run's end event", func() bool {
		endsMu.Lock()
		defer endsMu.Unlock()
		return runEnded
	})
	_, _ = stream.Stop()
	mu.Lock()
	if runs["started"] != 1 || runs["gated"] != 0 {
		t.Fatalf("runs %v, want started 1 and gated 0", runs)
	}
	mu.Unlock()
	endsMu.Lock()
	defer endsMu.Unlock()
	started, gate := ends["c1/1"], ends["c1/2"]
	if started == nil || gate == nil {
		t.Fatalf("end results c1/1 %v, c1/2 %v, want both", started, gate)
	}
	if codes := coreCodes(*started); !slices.Equal(codes, []string{"interrupted"}) || !started.IsError || started.HasStructuredOutput || !started.HasDetails {
		t.Fatalf("c1/1 end result = %+v, want an interrupted error that keeps its details and no structured output", started)
	}
	if codes := coreCodes(*gate); !slices.Equal(codes, []string{"abandoned"}) {
		t.Fatalf("c1/2 codes = %v, want abandoned", codes)
	}
}
