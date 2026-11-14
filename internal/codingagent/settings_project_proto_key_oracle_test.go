package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the project settings key row. Pi's deepMergeObjects copies the project settings over the global ones with `result[key] = ...`
// (packages/coding-agent/src/core/settings-manager.ts:75); for the key `__proto__` that assignment sets the prototype of the merged object, so the
// members of that value are read as settings although the merged object has no such key. PiG merges own entries: `__proto__` is one more unknown key
// and nothing under it is a setting. The project files `{"quietStartup":true}` and `{}` are the ordinary ones and must be equal on both sides.
func TestAProjectSettingsKeyNamedProtoSetsNoSetting(t *testing.T) {
	cases := []struct{ project, pi, pig string }{
		{`{"__proto__":{"quietStartup":true}}`, "true", "false"},
		{`{"quietStartup":true}`, "true", "true"},
		{`{}`, "false", "false"},
	}
	type probe struct {
		Cwd      string `json:"cwd"`
		AgentDir string `json:"agentDir"`
	}
	probes := make([]probe, 0, len(cases))
	for _, c := range cases {
		root := t.TempDir()
		p := probe{Cwd: filepath.Join(root, "proj"), AgentDir: filepath.Join(root, "agent")}
		for dir, text := range map[string]string{p.AgentDir: `{"theme":"dark"}`, filepath.Join(p.Cwd, ".pig"): c.project, filepath.Join(p.Cwd, ".pi"): c.project} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		probes = append(probes, p)
	}
	var pi []string
	pioracle.Run(t, `
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
emit(input.map(({ cwd, agentDir }) => String(SettingsManager.create(cwd, agentDir, { projectTrusted: true }).getQuietStartup())));`, probes, &pi)
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d probes", len(pi), len(cases))
	}
	for i, c := range cases {
		pig := NewSettingsManagerWithProjectTrust(probes[i].Cwd, probes[i].AgentDir, true).GetQuietStartup().String()
		t.Logf("project settings %s: getQuietStartup() Pi %s, PiG %s", c.project, pi[i], pig)
		if pi[i] != c.pi {
			t.Errorf("project settings %s: Pi answers %s, the record says %s", c.project, pi[i], c.pi)
		}
		if pig != c.pig {
			t.Errorf("project settings %s: GetQuietStartup() = %s, want %s", c.project, pig, c.pig)
		}
	}
}
