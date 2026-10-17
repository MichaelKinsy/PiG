package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// largeEditFile is a 230 KB source file and the same file with one line changed near its middle.
func largeEditFile() (string, string) {
	var b strings.Builder
	for i := 0; b.Len() < 230_000; i++ {
		fmt.Fprintf(&b, "\tfmt.Println(\"line %d of a large generated source file\")\n", i)
	}
	oldStr := b.String()
	at := strings.Index(oldStr, "line 2000 ")
	return oldStr, oldStr[:at] + "changed " + oldStr[at:]
}

// TestEditDiffViewsLargeFileAllocBudget pins the edit tool's diff views to the changed window: the display diff and the patch of a
// one-line change in a 230 KB file must not split, tokenize or copy the file (they allocated about 3 MB, several copies of it).
func TestEditDiffViewsLargeFileAllocBudget(t *testing.T) {
	oldStr, newStr := largeEditFile()
	var diff, patch string
	bytes := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			diff = GenerateDiffString(oldStr, newStr).Diff
			patch = GenerateUnifiedPatch("big.go", oldStr, newStr)
		}
	}).AllocedBytesPerOp()
	if !strings.Contains(diff, "changed line 2000 ") || !strings.Contains(patch, "+\tfmt.Println(\"changed line 2000 ") {
		t.Fatalf("unexpected diff views:\n%s\n%s", diff, patch)
	}
	if bytes > 16<<10 {
		t.Fatalf("diff and patch of a one-line change in a %d-byte file allocated %d bytes/op, want <= 16384", len(oldStr), bytes)
	}
}

func BenchmarkEditDiffViewsLargeFile(b *testing.B) {
	oldStr, newStr := largeEditFile()
	b.ReportAllocs()
	for b.Loop() {
		GenerateDiffString(oldStr, newStr)
		GenerateUnifiedPatch("big.go", oldStr, newStr)
	}
}

// BenchmarkEditToolLargeFile is one edit call on a 230 KB file: read, match, write, display diff and patch.
func BenchmarkEditToolLargeFile(b *testing.B) {
	oldStr, _ := largeEditFile()
	dir := b.TempDir()
	path := filepath.Join(dir, "big.go")
	tool := &EditTool{CWD: dir}
	forward, _ := json.Marshal(map[string]any{"path": "big.go", "edits": []map[string]string{{"oldText": "\"line 2000 ", "newText": "\"changed line 2000 "}}})
	back, _ := json.Marshal(map[string]any{"path": "big.go", "edits": []map[string]string{{"oldText": "\"changed line 2000 ", "newText": "\"line 2000 "}}})
	if err := os.WriteFile(path, []byte(oldStr), 0o644); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		args := forward
		if i%2 == 1 {
			args = back
		}
		i++
		if result, err := tool.Execute(b.Context(), "id", args, nil); err != nil || result.IsError {
			b.Fatalf("edit failed: %+v, %v", result, err)
		}
	}
}
