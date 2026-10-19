package host

// The generation tests track a sentinel with runtime.AddCleanup. The sentinel must be collectable on its own: a
// pointer-free object under 16 bytes comes from the tiny allocator, which packs several into one block and frees the
// block (and runs the cleanups of everything in it) only once every one of them is unreachable. A generation whose
// sentinel shared a block with a long-lived small allocation (a boxed number in a cell, say) then stayed "alive" for as
// long as that did, whatever Chord kept.

import (
	"runtime"
	"testing"
	"weak"
)

func TestGenerationSentinelIsCollectedWhateverIsAllocatedNextToIt(t *testing.T) {
	const sentinels = 4096
	weaks := make([]weak.Pointer[generationSentinel], sentinels)
	neighbors := make([]*int, sentinels)
	for index := range sentinels {
		weaks[index] = weak.Make(&generationSentinel{generation: index})
		neighbors[index] = new(int)
	}
	runtime.GC()
	retained := 0
	for _, pointer := range weaks {
		if pointer.Value() != nil {
			retained++
		}
	}
	runtime.KeepAlive(neighbors)
	if retained != 0 {
		t.Fatalf("%d of %d unreferenced sentinels survived a collection beside live small allocations", retained, sentinels)
	}
}
