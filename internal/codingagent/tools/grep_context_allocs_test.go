package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// maxContextAllocGrowth is how many more allocations a grep call may make with 200 context lines than with 2. A call allocates for its process and its output
// cap, not per row: a per-row allocation (a string per event, a line per context row) adds thousands over this fixture; the two calls measured 142 and 148.
const maxContextAllocGrowth = 25

// Grep's allocations do not grow with its context: ripgrep's events are parsed into reused buffers and the rows are joined from bytes, so 200 lines of
// context around every match cost about what 2 do.
func TestGrepAllocationsDoNotGrowWithContext(t *testing.T) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep is not installed")
	}
	dir := t.TempDir()
	for f := range 8 {
		var text strings.Builder
		for line := range 1000 {
			if line%50 == 0 {
				fmt.Fprintf(&text, "needle in line %d of file %d\n", line, f)
			} else {
				fmt.Fprintf(&text, "plain filler text for line %d of file %d\n", line, f)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.txt", f)), []byte(text.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tool := &GrepTool{CWD: dir, RgPath: rg}
	allocs := func(context int) float64 {
		args, _ := json.Marshal(map[string]any{"pattern": "needle", "context": context, "limit": 1000})
		return testing.AllocsPerRun(5, func() {
			if result, err := tool.Execute(t.Context(), "id", args, nil); err != nil || result.IsError {
				t.Fatal(err, result.Content)
			}
		})
	}
	small, large := allocs(2), allocs(200)
	t.Logf("allocations per call: %v with context 2, %v with context 200", small, large)
	if large > small+maxContextAllocGrowth {
		t.Fatalf("a call with context 200 makes %v allocations against %v with context 2: grep allocates per context row again", large, small)
	}
}
