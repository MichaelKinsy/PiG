package sdk

import (
	"strings"
	"testing"
)

// TruncateHead and TruncateTail return a substring of their input, so cutting a large output builds no per-line strings and joins nothing.
func TestTruncationCutsWithoutCopying(t *testing.T) {
	content := strings.Repeat("a line of command output\n", 8000)
	for name, cut := range map[string]func(){
		"tail": func() { _ = TruncateTail(content, TruncationOptions{}) },
		"head": func() { _ = TruncateHead(content, TruncationOptions{}) },
	} {
		if allocs := testing.AllocsPerRun(20, cut); allocs != 0 {
			t.Errorf("%s truncation of 8000 lines: %v allocations, want 0", name, allocs)
		}
	}
}
