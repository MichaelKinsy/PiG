//go:build !race

package ai

import (
	"runtime"
	"strings"
	"testing"
)

// The most objects one streamed tool-argument delta of a managed builder may allocate while nothing reads the
// publications (5.0 after lw-quiet), and what the base (b02c2ceabc) allocated, which parsed the partial arguments on
// every delta.
const (
	unreadArgumentDeltaAllocsBase    = 23.8
	unreadArgumentDeltaAllocsCeiling = 8
)

// While nothing reads a managed builder's publications, a tool-argument delta is appended and published, not parsed:
// the slope of the allocations over the number of deltas stays under the ceiling.
func TestUnreadToolArgumentDeltasAllocateLittle(t *testing.T) {
	run := func(deltas int) uint64 {
		text := `{"path":"out.ts","content":"` + strings.Repeat(`export const value = \"x\";\n`, deltas)
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, "p", "m")
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		runManagedBuilder(builder, func() {
			for start := 0; start < deltas*16; start += 16 {
				builder.toolCallDelta(streamToolCallDelta{index: 0, id: "c", name: "write", argumentsDelta: text[start : start+16]})
			}
		})
		runtime.ReadMemStats(&after)
		return after.Mallocs - before.Mallocs
	}
	const small, large = 1_000, 5_000
	run(small)
	perDelta := (float64(run(large)) - float64(run(small))) / float64(large-small)
	t.Logf("allocations per delta: %.1f (base %.1f, ceiling %.1f)", perDelta, float64(unreadArgumentDeltaAllocsBase), float64(unreadArgumentDeltaAllocsCeiling))
	if perDelta > unreadArgumentDeltaAllocsCeiling {
		t.Fatalf("allocations per unread argument delta = %.1f, want at most %.1f (base %.1f)", perDelta, float64(unreadArgumentDeltaAllocsCeiling), float64(unreadArgumentDeltaAllocsBase))
	}
}
