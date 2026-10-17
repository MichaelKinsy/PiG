//go:build !race

package tui

import "testing"

// Moving the cursor a word forward reads the text through a push loop, as iter.Pull started a coroutine for each keystroke: 10 allocations then, 7 now. The ceiling only goes down.
func TestFindWordForwardAllocationCeiling(t *testing.T) {
	const text = "  hello, world! some.words and (punctuation) here"
	got := testing.AllocsPerRun(200, func() { FindWordForward(text, 0) })
	t.Logf("%.1f allocs", got)
	if got > wordForwardAllocCeiling {
		t.Fatalf("FindWordForward allocates %v times, ceiling %d", got, wordForwardAllocCeiling)
	}
}

const wordForwardAllocCeiling = 7
