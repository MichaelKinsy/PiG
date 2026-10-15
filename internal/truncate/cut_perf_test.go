package truncate

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A truncated result is a substring of its input: neither cut builds the lines, so neither allocates however many lines the input has.
func TestTruncationCutsWithoutCopying(t *testing.T) {
	content := strings.Repeat("a line of command output\n", 8000)
	if allocs := testing.AllocsPerRun(20, func() { _ = TruncateTail(content, DefaultMaxBytes, DefaultMaxLines) }); allocs != 0 {
		t.Errorf("tail truncation of 8000 lines: %v allocations, want 0", allocs)
	}
	if allocs := testing.AllocsPerRun(20, func() { _ = TruncateHead(content, DefaultMaxBytes, DefaultMaxLines) }); allocs != 0 {
		t.Errorf("head truncation of 8000 lines: %v allocations, want 0", allocs)
	}
}

// TailWindow.Append costs the chunk, not the window: a full window takes a small chunk without moving the bytes it keeps. The cost of a 7-byte append
// must not scale with the window (a 1 MiB window against a 1 KiB one).
func TestTailWindowAppendCostDoesNotGrowWithTheWindow(t *testing.T) {
	perAppend := func(maxBytes int) float64 {
		window := NewTailWindow(maxBytes, DefaultMaxLines)
		window.Append(strings.Repeat("0123456789\n", maxBytes/5))
		result := testing.Benchmark(func(b *testing.B) {
			for b.Loop() {
				window.Append("012345\n")
			}
		})
		return float64(result.T.Nanoseconds()) / float64(result.N)
	}
	small, large := perAppend(1<<10), perAppend(1<<20)
	testenv.TimingGateFatal(t, large > 8*small+50, "append to a full 1 MiB window costs %.0f ns, a 1 KiB one %.0f ns: the cost scales with the window", large, small)
}

// Result copies only the kept text, once.
func TestTailWindowResultCopiesOnlyTheCut(t *testing.T) {
	window := NewTailWindow(DefaultMaxBytes, DefaultMaxLines)
	for range 400 {
		window.Append(strings.Repeat("a line of command output\n", 20))
	}
	if allocs := testing.AllocsPerRun(20, func() { _ = window.Result() }); allocs > 1 {
		t.Fatalf("Result: %v allocations, want at most 1 (the kept text)", allocs)
	}
}
