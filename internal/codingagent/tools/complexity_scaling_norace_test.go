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
	// grep takes limit x (2*context+1) lines from ripgrep and keeps its output only up to the 50KB cap (grep.ts, Pi 8da141c2a, #9276), so with the file fixed the memory does not grow with the context.
	// Both sizes must lie past the cap: below it the output, and with it the bytes allocated, grow with the context until the cap is reached (n=100 gave 92KB and n=1000 204KB, a slope of 0.347 against the tolerance of 0.35), and how many context lines rg
	// has written when the tool kills it after the limit can depend on the load, so a size under the cap sits within 0.003 of the tolerance.
	bigDir := t.TempDir()
	scalingFile(t, bigDir, 20000)
	testscaling.Check(t, testscaling.Case{Name: "grep 100 matches with context n in a 20,000-line file", Size: 2000, Bound: testscaling.Constant, Prepare: func(n int) func() {
		args, _ := json.Marshal(map[string]any{"pattern": "match", "context": n, "limit": 100})
		tool := &GrepTool{CWD: bigDir, RgPath: rg}
		return func() {
			// The output must be the 100-match context listing, or the gate measures an error or no-match path (an rg that fails under the test's HOME reports "No matches found").
			result, err := tool.Execute(context.Background(), "", args, nil)
			if err != nil || result.IsError {
				panic(fmt.Sprint(err, result.Content))
			}
			if details, ok := result.Details.(*GrepDetails); !ok || details.MatchLimitReached != 100 {
				panic(fmt.Sprintf("grep did not list 100 matches with context: %v", result.Content))
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
