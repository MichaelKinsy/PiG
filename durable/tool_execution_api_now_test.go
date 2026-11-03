package durable_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi harness/types.ts ToolExecutionApi.now: the Harness clock (HarnessOptions.now), for times a rerun after a restart
// compares against. Every read goes to the clock; once the call settled the api rejects, as upstream's now() throws.
func TestToolExecutionApiNowReadsTheHarnessClockAndThrowsAfterTheCallSettled(t *testing.T) {
	const base = 1_650_000_000_000.0
	var ticks atomic.Int64
	clock := func() float64 { return base + float64(ticks.Add(1)) }

	var mu sync.Mutex
	var reads []float64
	var escaped durable.ToolExecutionApi
	var clockCalls [2]int64
	clockTool := &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "clock", Description: "Reads the Harness clock", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(_ context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			mu.Lock()
			defer mu.Unlock()
			clockCalls[0] = ticks.Load()
			reads = append(reads, api.Now())
			clockCalls[1] = ticks.Load()
			reads = append(reads, api.Now())
			escaped = api
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: "read"}}}, nil
		},
	}

	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("clock", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}}),
	})
	registry := harness.CreateRegistry()
	if err := registry.Install(&durable.Extension{Name: "clock", Tools: []*durable.ToolRegistration{clockTool}}); err != nil {
		t.Fatal(err)
	}
	opened, err := harness.OpenHarness(t.Context(), storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opened.Close(t.Context()) }()
	root, err := opened.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("what time is it")})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(t.Context())
	if err != nil || settled.Status != durable.SubmissionDone {
		t.Fatalf("submission = %+v, %v", settled, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reads) != 2 {
		t.Fatalf("the tool read the clock %d times, want 2", len(reads))
	}
	if reads[0] != base+float64(clockCalls[0]+1) || reads[1] != base+float64(clockCalls[1]+1) {
		t.Fatalf("reads %v, want each api.Now() to be the very next reading of HarnessOptions.Now (%v and %v)", reads, base+float64(clockCalls[0]+1), base+float64(clockCalls[1]+1))
	}
	if !(reads[1] > reads[0]) {
		t.Fatalf("reads %v, want a later read to see a later clock", reads)
	}
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("api.Now() after the call settled returned, want it to throw")
		}
		if message := fmt.Sprint(recovered); message == "" {
			t.Fatal("api.Now() after the call settled panicked without a message")
		}
	}()
	escaped.Now()
}
