package tools

import (
	"fmt"
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
// one-line change in a 230 KB file must not split or copy the file (they allocated about 1 MB, split per part and line).
func TestEditDiffViewsLargeFileAllocBudget(t *testing.T) {
	oldStr, newStr := largeEditFile()
	var diff, patch string
	bytes := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			diff, _ = GenerateDiffString(oldStr, newStr)
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
