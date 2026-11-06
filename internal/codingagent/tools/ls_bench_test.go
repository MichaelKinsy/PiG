package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkLsTenThousandNames is the whole ls tool call over a directory of 10,000 mixed-case, accented and CJK names: sorting weighs every
// name once, so this is where the collation tables' load and per-name cost show.
func BenchmarkLsTenThousandNames(b *testing.B) {
	dir := b.TempDir()
	stems := []string{"File_%05d_Name.TXT", "résumé_%05d.md", "Café-%05d.go", "日本語_%05d.txt", "src-%05d", "README_%05d", "Zürich %05d.pdf", "ångström_%05d.dat"}
	for i := range 10000 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf(stems[i%len(stems)], (i*7919)%10000)), nil, 0o644); err != nil {
			b.Fatal(err)
		}
	}
	tool := &LsTool{CWD: dir}
	args := []byte(`{"path":".","limit":1}`)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if result, err := tool.Execute(b.Context(), "id", args, nil); err != nil || result.IsError {
			b.Fatalf("ls failed: %+v, %v", result, err)
		}
	}
}

// BenchmarkLsSortTenThousandNames is the sort alone (lowercase, weigh, order) without reading the directory.
func BenchmarkLsSortTenThousandNames(b *testing.B) {
	stems := []string{"File_%05d_Name.TXT", "résumé_%05d.md", "Café-%05d.go", "日本語_%05d.txt", "src-%05d", "README_%05d", "Zürich %05d.pdf", "ångström_%05d.dat"}
	names := make([]string, 10000)
	for i := range names {
		names[i] = fmt.Sprintf(stems[i%len(stems)], (i*7919)%10000)
	}
	work := make([]string, len(names))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		copy(work, names)
		sortEntriesLikeLocaleCompare(work)
	}
}
