package tools

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Ported from upstream test/tools.test.ts "should ignore a leading ./ in the pattern" (#12020).
func TestFindIgnoresLeadingDotSlashInPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"src/a.ts": "a", "package.json": "{}"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, pattern := range []string{"./src/*.ts", "./src/**/*.ts", "././src/*.ts"} {
		if got := runFind(t, dir, map[string]any{"pattern": pattern, "path": dir}).Text(); got != "src/a.ts" {
			t.Fatalf("pattern %q = %q, want src/a.ts", pattern, got)
		}
	}
	// Without a "/" left, "./*.json" matches basenames at any depth, like "*.json".
	if err := os.WriteFile(filepath.Join(dir, "src", "b.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := outputLines(runFind(t, dir, map[string]any{"pattern": "./*.json", "path": dir}).Text())
	slices.Sort(got)
	if !slices.Equal(got, []string{"package.json", "src/b.json"}) {
		t.Fatalf("./*.json = %q", got)
	}
}
