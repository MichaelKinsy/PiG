//go:build !race

package tui

import (
	"io"
	"strings"
	"testing"
)

// bytesOnlyWriter has no WriteString, as the writers of a terminal and of a test frame are.
type bytesOnlyWriter struct{ n int }

func (w *bytesOnlyWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }

// A write to the user's terminal through a writer that takes bytes only (no WriteString) costs no allocation: WriteToTerminal replaced fmt.Fprint, which copies into a pooled buffer, and
// io.WriteString converted every string to a new slice, two more allocations a frame for the cursor's hide and show (BenchmarkStatusLifecycle/regular/editor=embedded: 61 to 65
// allocs/op, bisected to 3cc0c71d3).
func TestWriteToTerminalOfAByteWriterDoesNotAllocate(t *testing.T) {
	t.Run("a bare writer", func(t *testing.T) { writeToTerminalAllocs(t, &bytesOnlyWriter{}) })
	t.Run("a renderer's stdout over it", func(t *testing.T) { writeToTerminalAllocs(t, stdoutWriter(&bytesOnlyWriter{})) })
}

func writeToTerminalAllocs(t *testing.T, out io.Writer) {
	if allocs := testing.AllocsPerRun(100, func() { WriteToTerminal(out, "\x1b[?25l") }); allocs != 0 {
		t.Fatalf("allocs = %v, want 0", allocs)
	}
}

// A write the pool keeps no buffer for (larger than maxPooledTerminalWrite) costs its copy and nothing else. A pooled buffer for it cost a second
// allocation, the holder the pool lost with every such write: one more for each chunk of a full redraw, ten for a transcript of 5,000 turns
// (BenchmarkProcessTerminalFullRedrawCJK/turns=5000 with the collector off: 322 to 332, bisected to 62f0353e7).
func TestWriteToTerminalOfALargeChunkCopiesItOnce(t *testing.T) {
	chunk := strings.Repeat("x", maxPooledTerminalWrite+1)
	for name, out := range map[string]io.Writer{"a bare writer": &bytesOnlyWriter{}, "a renderer's stdout over it": stdoutWriter(&bytesOnlyWriter{})} {
		t.Run(name, func(t *testing.T) {
			if allocs := testing.AllocsPerRun(100, func() { WriteToTerminal(out, chunk) }); allocs != 1 {
				t.Fatalf("allocs = %v, want 1: the copy", allocs)
			}
		})
	}
}
