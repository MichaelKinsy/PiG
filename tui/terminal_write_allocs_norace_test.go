//go:build !race

package tui

import (
	"io"
	"testing"
)

// bytesOnlyWriter has no WriteString, as the writers of a terminal and of a test frame are.
type bytesOnlyWriter struct{ n int }

func (w *bytesOnlyWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }

// A write to the user's terminal through a writer that takes bytes only (no WriteString) costs no allocation: WriteToTerminal replaced fmt.Fprint, which copies into a pooled buffer, and
// io.WriteString converted every string to a new slice, two more allocations a frame for the cursor's hide and show (BenchmarkStatusLifecycle/regular/editor=embedded: 61 to 65
// allocs/op, bisected to 3cc0c71d3).
func TestWriteToTerminalOfAByteWriterDoesNotAllocate(t *testing.T) {
	writeToTerminalAllocs(t, &bytesOnlyWriter{})
}

func writeToTerminalAllocs(t *testing.T, out io.Writer) {
	if allocs := testing.AllocsPerRun(100, func() { WriteToTerminal(out, "\x1b[?25l") }); allocs != 0 {
		t.Fatalf("allocs = %v, want 0", allocs)
	}
}
