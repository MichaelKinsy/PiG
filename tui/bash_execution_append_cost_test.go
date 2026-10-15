package tui

import (
	"strings"
	"testing"
)

// The bash executor hands AppendOutput text it already stripped of ANSI sequences and carriage returns. Appending a clean
// chunk must not copy it to bytes and back to strip it again: the only copies left are the block's own buffers.
func TestBashExecutionAppendOutputDoesNotCopyCleanChunks(t *testing.T) {
	chunk := strings.Repeat("clean line of command output\n", 1024)
	block := NewBashExecutionComponent("yes", nil, false, 1)
	allocs := testing.AllocsPerRun(200, func() { block.AppendOutput(chunk) })
	t.Logf("%.2f allocations per %d-byte clean chunk", allocs, len(chunk))
	if allocs >= 1 {
		t.Fatalf("AppendOutput copies a clean chunk: %.2f allocations per chunk", allocs)
	}
}
