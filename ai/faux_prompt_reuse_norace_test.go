//go:build !race

package ai

import (
	"bytes"
	"runtime"
	"testing"
)

// A session's later requests write their remembered messages over the storage of the ones before, and a message the previous request
// had is not written out again, so the prompt (the size of the whole history) is not reallocated on every model call.
func TestFauxEstimateReusesTheSessionPromptStorage(t *testing.T) {
	provider := NewFauxProvider(FauxConfig{})
	request := fauxHistory(500)
	options := StreamOptions{SessionID: "s"}
	content := []FauxContentBlock{FauxText("ok")}
	provider.estimateUsage(request, options, content)
	provider.estimateUsage(request, options, content)
	var prompt bytes.Buffer
	writeFauxPromptRest(&prompt, provider.promptEntry("s").messages, false)
	promptBytes := float64(prompt.Len())
	const calls = 5
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range calls {
		provider.estimateUsage(request, options, content)
	}
	runtime.ReadMemStats(&after)
	if perCall := float64(after.TotalAlloc-before.TotalAlloc) / calls; perCall > promptBytes/4 {
		t.Fatalf("each estimate allocated %.0f bytes for a %.0f-byte prompt: the prompt storage is not reused", perCall, promptBytes)
	}
}

// SafeJsonStringify keeps its encoder between calls. A three-key object cost 14 allocations with a fresh encoder and a clone
// of the canonical text; what remains is encoding/json's own map walk.
func TestSafeJsonStringifyAllocationCeiling(t *testing.T) {
	value := JsonObject{"n": float64(7), "path": "a/b", "flag": true}
	if got := SafeJsonStringify(value); got != `{"flag":true,"n":7,"path":"a/b"}` {
		t.Fatalf("got %s", got)
	}
	const ceiling = 10
	allocs := testing.AllocsPerRun(100, func() { _ = SafeJsonStringify(value) })
	t.Logf("%.0f allocs", allocs)
	if allocs > ceiling {
		t.Fatalf("SafeJsonStringify allocates %.0f objects, ceiling %d", allocs, ceiling)
	}
}
