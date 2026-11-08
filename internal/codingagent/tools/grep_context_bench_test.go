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

// BenchmarkGrepToolContext is one grep call with context 2 over 40 files of 2000 lines, each with a match every 50 lines (the call Pi #9276 ran out of memory on at 100 MB).
func BenchmarkGrepToolContext(b *testing.B) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		b.Skip("ripgrep is not installed")
	}
	dir := b.TempDir()
	for f := range 40 {
		var text strings.Builder
		for line := range 2000 {
			if line%50 == 0 {
				fmt.Fprintf(&text, "needle in line %d of file %d\n", line, f)
			} else {
				fmt.Fprintf(&text, "plain filler text for line %d of file %d\n", line, f)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", f)), []byte(text.String()), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	tool := &GrepTool{CWD: dir, RgPath: rg}
	args, _ := json.Marshal(map[string]any{"pattern": "needle", "context": 2, "limit": 1000})
	b.ReportAllocs()
	for b.Loop() {
		if result, err := tool.Execute(b.Context(), "id", args, nil); err != nil || result.IsError {
			b.Fatal(err, result.Content)
		}
	}
}
