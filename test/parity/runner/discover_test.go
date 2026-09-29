//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverScenarios_RecursiveBehaviorDirs(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("model/03-model-invalid.toml", "name='a'\ncovers=['packages/coding-agent/src/main.ts']\ndriver='interactive-tmux'\n")
	mustWrite("settings/02-settings.toml", "name='b'\ncovers=['packages/coding-agent/src/main.ts']\ndriver='interactive-tmux'\n")
	mustWrite("legacy/ignore.tmux", "tmux send-keys -t \"$SESSION\" 'hi'\n")

	scenarios, err := DiscoverScenarios(dir)
	if err != nil {
		t.Fatalf("DiscoverScenarios: %v", err)
	}
	if len(scenarios) != 2 {
		t.Fatalf("len(scenarios) = %d, want 2", len(scenarios))
	}
	if got := filepath.Base(scenarios[0].SourcePath); got != "03-model-invalid.toml" {
		t.Fatalf("scenarios[0] = %q", got)
	}
	if got := filepath.Base(scenarios[1].SourcePath); got != "02-settings.toml" {
		t.Fatalf("scenarios[1] = %q", got)
	}
}
