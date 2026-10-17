package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// types.ts ToolExecutionApi.memo (harness/types.ts:192-193): the one-argument form reads a durable memo of the tool task
// (absent before any write); the candidate form stores the candidate unless a memo exists and returns the durable winner,
// so a second candidate loses to the first.
// mutation-checked: zeroing the results of ToolExecutionApi.Memo fails it
// Pi: packages/durable/src/harness/types.ts:192 (memo)
func TestToolExecutionApiMemoReadsAbsentThenTheFirstCandidateWins(t *testing.T) {
	setup := chatSetup(t)
	type observed struct {
		beforePresent bool
		first, second durable.JsonValue
		afterPresent  bool
		after         durable.JsonValue
		err           error
	}
	var got observed
	addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "remember", Description: "Memoizes", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			var err error
			_, got.beforePresent, err = api.Memo(ctx, "k")
			if err == nil {
				got.first, err = api.MemoCandidate(ctx, "k", "one")
			}
			if err == nil {
				got.second, err = api.MemoCandidate(ctx, "k", "two")
			}
			if err == nil {
				got.after, got.afterPresent, err = api.Memo(ctx, "k")
			}
			got.err = err
			return durable.ToolExecutionResult{}, err
		},
	}))
	setup.Faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("remember", map[string]any{}, &ai.FauxToolCallOptions{ID: "c1"})}, StopReason: "toolUse"}),
		fauxAnswer("done"),
	})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	submission := submitWhenBusy(t, root, "go")
	must(submission.Wait(testContext))
	drained(harness)
	if got.err != nil {
		t.Fatalf("memo calls failed: %v", got.err)
	}
	if got.beforePresent || !got.afterPresent {
		t.Fatalf("memo present before = %v, after = %v; want false, true", got.beforePresent, got.afterPresent)
	}
	if got.first != "one" || got.second != "one" || got.after != "one" {
		t.Fatalf("memo values = %v, %v, %v; want the first candidate three times", got.first, got.second, got.after)
	}
	closeHarness(t, harness)
}
