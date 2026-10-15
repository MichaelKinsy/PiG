package ai

import (
	"errors"
	"runtime"
	"testing"
)

// The request build must be linear in the history: every API converts a tool-using history of 100 and of 800 turns, and the allocations and allocated bytes per turn must stay flat. A pass that allocates per message in proportion to the history (a copy of the history, an index rebuilt per tool call) makes the per-turn cost grow with the history and fails the bound; a constant factor does not. A rescan that allocates nothing is not measured here.
func TestRequestBuildAllocationsAreLinearInTheHistory(t *testing.T) {
	for _, spec := range []string{"anthropic/claude-sonnet-4-5", "openai/gpt-5", "groq/llama-3.1-8b-instant", "google/gemini-2.5-flash", "mistral/devstral-medium-latest"} {
		t.Run(spec, func(t *testing.T) {
			model, ok := LookupModelExact(spec)
			if !ok {
				t.Fatalf("model %s is not in the catalog", spec)
			}
			provider := newMatrixProvider(t, model, "http://127.0.0.1:1", false)
			t.Cleanup(func() { _ = provider.Close() })
			perTurn := func(turns int) (float64, float64) {
				messages := transformHistory(turns)
				for i := range messages {
					if assistant, ok := messages[i].(AssistantMessage); ok {
						assistant.API, assistant.Provider, assistant.Model = model.API, model.Provider, model.ID
						messages[i] = assistant
					}
				}
				transcript := NormalizeContext(Context{Messages: messages})
				options := StreamOptions{Transport: TransportSSE, OnPayload: func(any, *Model) (any, error) { return nil, errors.New("stop") }}
				build := func() {
					if stream, err := provider.Stream(t.Context(), transcript, options); err == nil && stream != nil {
						_ = stream.Result()
					}
				}
				allocs, bytes := requestBuildCost(build)
				return allocs / float64(turns), bytes / float64(turns)
			}
			smallAllocs, smallBytes := perTurn(100)
			largeAllocs, largeBytes := perTurn(800)
			if largeAllocs > smallAllocs*1.25 || largeBytes > smallBytes*1.5 {
				t.Fatalf("cost per turn grew from %.1f allocations and %.0f bytes (100 turns) to %.1f allocations and %.0f bytes (800 turns): the request build is not linear", smallAllocs, smallBytes, largeAllocs, largeBytes)
			}
			t.Logf("per turn: %.1f allocations and %.0f bytes at 100 turns, %.1f and %.0f at 800", smallAllocs, smallBytes, largeAllocs, largeBytes)
		})
	}
}

// requestBuildCost returns the mean allocations and allocated bytes of one build. The allocation count alone misses a copy of the history per message: that is one allocation whose size grows with the history.
func requestBuildCost(build func()) (allocs, bytes float64) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const runs = 3
	build()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		build()
	}
	runtime.ReadMemStats(&after)
	return float64(after.Mallocs-before.Mallocs) / runs, float64(after.TotalAlloc-before.TotalAlloc) / runs
}
