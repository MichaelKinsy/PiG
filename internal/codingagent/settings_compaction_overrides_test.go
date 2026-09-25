package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettingsLayers(t *testing.T, global, project string) *SettingsManager {
	t.Helper()
	agentDir, cwd := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	if project != "" {
		if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, CONFIG_DIR_NAME, "settings.json"), []byte(project), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return NewSettingsManager(cwd, agentDir)
}

// Upstream resolves each compaction token setting through the exact
// "provider/modelId" override, then the ordinary setting, then the default.
// Project overrides deep-merge into global ones field by field.
func TestCompactionModelOverrides(t *testing.T) {
	sm := writeSettingsLayers(t,
		`{"compaction":{"reserveTokens":9000,"modelOverrides":{"anthropic/claude-x":{"reserveTokens":5000,"keepRecentTokens":4000}}}}`,
		`{"compaction":{"modelOverrides":{"anthropic/claude-x":{"keepRecentTokens":0}}}}`)
	got := sm.GetModelCompactionSettings("anthropic", "claude-x")
	if got.ReserveTokens != 5000 || got.KeepRecentTokens != 0 || !got.Enabled {
		t.Fatalf("override settings = %+v, want reserveTokens 5000, keepRecentTokens 0", got)
	}
	other := sm.GetModelCompactionSettings("openai", "gpt-x")
	if other.ReserveTokens != 9000 || other.KeepRecentTokens != 20000 {
		t.Fatalf("settings without an override = %+v, want the ordinary 9000 and the default 20000", other)
	}
	if plain := sm.GetCompactionSettings(); plain.ReserveTokens != 9000 {
		t.Fatalf("settings without a model = %+v, want the ordinary reserveTokens", plain)
	}
}
