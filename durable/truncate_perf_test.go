package durable

import (
	"strings"
	"testing"
)

// TruncateHead returns a prefix of its input, so cutting a large output builds no per-line strings and joins nothing.
func TestTruncateHeadCutsWithoutCopying(t *testing.T) {
	content := strings.Repeat("a line of command output\n", 8000)
	if allocs := testing.AllocsPerRun(20, func() { _ = TruncateHead(content, TruncationOptions{}) }); allocs != 0 {
		t.Fatalf("head truncation of 8000 lines: %v allocations, want 0", allocs)
	}
}
