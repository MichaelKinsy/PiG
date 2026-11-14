package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the legacy API key row. Pi's migrateAuthToAuthJson moves the keys of settings.json `apiKeys` to auth.json unless `migrated[provider]` is set
// already, on a plain object (packages/coding-agent/src/migrations.ts:54), so the key of a provider named like a member of Object.prototype is not moved
// and is gone with `apiKeys`, which the migration deletes. PiG's migrated credentials are own entries: such a key is moved like any other. The second
// settings file holds ordinary names only and must give the same auth.json on both sides.
func TestALegacyApiKeyOfAProviderNamedLikeAnObjectPrototypeMemberIsMigrated(t *testing.T) {
	cases := []struct {
		settings string
		pi       []string
		pig      []string
	}{
		{`{"apiKeys":{"constructor":"k1","openai":"k2","toString":"k3"}}`, []string{"openai"}, []string{"constructor", "openai", "toString"}},
		{`{"apiKeys":{"local":"k4","openai":"k2"}}`, []string{"local", "openai"}, []string{"local", "openai"}},
	}
	type answer struct {
		Moved []string `json:"moved"`
		Auth  string   `json:"auth"`
	}
	for _, c := range cases {
		piDir, pigDir := t.TempDir(), t.TempDir()
		for _, dir := range []string{piDir, pigDir} {
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(c.settings), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var pi answer
		pioracle.Run(t, `
const { migrateAuthToAuthJson } = await load("pi-coding-agent/migrations.js");
const fs = await import("node:fs");
const moved = migrateAuthToAuthJson();
const path = input + "/auth.json";
emit({ moved, auth: fs.existsSync(path) ? fs.readFileSync(path, "utf8").replace(/\s+/g, "") : "" });`, piDir, &pi, "PI_CODING_AGENT_DIR="+piDir, "PIG_CODING_AGENT_DIR="+piDir, "PI_HOME="+piDir, "PIG_HOME="+piDir, "HOME="+piDir)
		moved, err := migrateAuthToAuthJSON(pigDir)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(filepath.Join(pigDir, "auth.json"))
		auth := strings.Join(strings.Fields(string(data)), "")
		t.Logf("settings %s: Pi moved %v, auth.json %s; PiG moved %v, auth.json %s", c.settings, pi.Moved, pi.Auth, moved, auth)
		if !slices.Equal(pi.Moved, c.pi) {
			t.Errorf("settings %s: Pi moved %v, the record says %v", c.settings, pi.Moved, c.pi)
		}
		if !slices.Equal(moved, c.pig) {
			t.Errorf("settings %s: PiG moved %v, want %v", c.settings, moved, c.pig)
		}
		for _, provider := range c.pig {
			if inPi := slices.Contains(c.pi, provider); strings.Contains(pi.Auth, `"`+provider+`":{"type":"api_key"`) != inPi || !strings.Contains(auth, `"`+provider+`":{"type":"api_key"`) {
				t.Errorf("settings %s, provider %s: Pi's auth.json %s (the record says it holds the provider: %v), PiG's %s (want the provider)", c.settings, provider, pi.Auth, inPi, auth)
			}
		}
	}
}
