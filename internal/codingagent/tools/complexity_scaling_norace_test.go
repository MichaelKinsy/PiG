//go:build !race

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Complexity gates for the built-in tools. Child processes (rg, fd) are not in the measured allocations; the gates measure what PiG does with their output.

func scalingFile(t *testing.T, dir string, lines int) string {
	t.Helper()
	path := filepath.Join(dir, "big.txt")
	var content strings.Builder
	for i := range lines {
		fmt.Fprintf(&content, "line %d of the file with a match token\n", i)
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScalingTools(t *testing.T) {
	rg := requireRG(t)
	// grep formats limit x (2*context+1) lines for an output it cuts to 50KB (grep.ts:176-282). PiG measures the lines past those 50KB instead of building them (#9276), so with the file fixed the memory does not grow with the context.
	bigDir := t.TempDir()
	scalingFile(t, bigDir, 20000)
	testscaling.Check(t, testscaling.Case{Name: "grep 100 matches with context n in a 20,000-line file", Size: 100, Bound: testscaling.Constant, Prepare: func(n int) func() {
		args, _ := json.Marshal(map[string]any{"pattern": "match", "context": n, "limit": 100})
		tool := &GrepTool{CWD: bigDir, RgPath: rg}
		return func() {
			// The output must be the byte-truncated context listing, or the gate measures an error or no-match path (an rg that fails under the test's HOME reports "No matches found").
			result, err := tool.Execute(context.Background(), "", args, nil)
			if err != nil || result.IsError {
				panic(fmt.Sprint(err, result.Content))
			}
			if details, ok := result.Details.(*GrepDetails); !ok || details.Truncation == nil || details.MatchLimitReached != 100 {
				panic(fmt.Sprintf("grep did not format 100 matches with context into a truncated output: %v", result.Content))
			}
		}
	}})
	// read loads the whole file to count its lines, then returns one page (read.ts does the same).
	testscaling.Check(t, testscaling.Case{Name: "read first 10 lines of an n-line file", Size: 1000, Bound: testscaling.Linear, Prepare: func(n int) func() {
		dir := t.TempDir()
		scalingFile(t, dir, n)
		args := json.RawMessage(`{"path":"big.txt","limit":10}`)
		tool := &ReadTool{CWD: dir}
		return func() {
			if result, err := tool.Execute(context.Background(), "", args, nil); err != nil || result.IsError {
				panic(fmt.Sprint(err, result.Content))
			}
		}
	}})
	// edit finds one unique old text in the file and writes the file back.
	testscaling.Check(t, testscaling.Case{Name: "edit one unique line in an n-line file", Size: 1000, Bound: testscaling.Linear, Prepare: func(n int) func() {
		dir := t.TempDir()
		scalingFile(t, dir, n)
		target := fmt.Sprintf("line %d of the file", n/2)
		args, _ := json.Marshal(map[string]any{"path": "big.txt", "edits": []map[string]string{{"oldText": target + " with", "newText": target + " without"}}})
		tool := &EditTool{CWD: dir}
		return func() {
			if result, err := tool.Execute(context.Background(), "", args, nil); err != nil || result.IsError {
				panic(fmt.Sprint(err, result.Content))
			}
		}
	}})
}
