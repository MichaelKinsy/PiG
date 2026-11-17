// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build !race

package tui

import (
	"io"
	"strings"
	"testing"
)

// Writing a frame chunk through a renderer's writer allocates nothing: before terminalWriter had a WriteString, a chunk of 64 KiB or less cost the
// string Write built from the pooled bytes, and a larger chunk cost that string and the byte copy before it (BenchmarkTerminalWriteThroughProcessTerminal,
// BenchmarkProcessTerminalFullRedrawCJK).
func TestWritingAFrameChunkThroughATerminalWriterAllocatesNothing(t *testing.T) {
	for _, size := range []int{512, maxPooledTerminalWrite, maxPooledTerminalWrite + 1, 2 << 20} {
		chunk := strings.Repeat("x", size)
		var out io.Writer = terminalWriter{&stringTerminal{}} // the renderer holds its writer as an io.Writer: converting it per call would count an allocation of the test's
		if allocs := testing.AllocsPerRun(200, func() { _, _ = writeString(out, chunk) }); allocs != 0 {
			t.Errorf("a %d byte chunk allocated %v times, want 0", size, allocs)
		}
	}
}
