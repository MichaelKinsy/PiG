//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLiveAuthUsesOnlyExplicitEnv(t *testing.T) {
	for _, helper := range []struct {
		name     string
		home     func(*testing.T) string
		authPath string
	}{
		{"pig", extIsolatedHome, filepath.Join("agent", "auth.json")},
		{"pi", upstreamIsolatedHome, filepath.Join(".pi", "agent", "auth.json")},
	} {
		for _, tc := range []struct{ name, key string }{{"missing", ""}, {"configured", "explicit-test-bearer"}} {
			key := tc.key
			var child *testing.T
			t.Run(helper.name+"/"+tc.name, func(t *testing.T) {
				child = t
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("COPILOT_GITHUB_TOKEN", key)
				// Ambient auth must neither enable a credential-free run nor replace the supplied bearer.
				for _, root := range []string{".pig", ".pi"} {
					dir := filepath.Join(home, root, "agent")
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"github-copilot":{"type":"oauth","access":"ambient-access","refresh":"ambient-refresh","expires":9999999999999}}`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				root := helper.home(t)
				if key == "" {
					t.Fatal("missing explicit credential did not skip")
				}
				data, err := os.ReadFile(filepath.Join(root, helper.authPath))
				if err != nil {
					t.Fatal(err)
				}
				var got map[string]map[string]string
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatal(err)
				}
				if len(got) != 1 || len(got["github-copilot"]) != 2 || got["github-copilot"]["type"] != "api_key" || got["github-copilot"]["key"] != key {
					t.Fatal("temporary auth does not contain only the explicit API-key credential")
				}
			})
			if child.Skipped() != (key == "") {
				t.Errorf("%s: skipped=%v, want %v", helper.name, child.Skipped(), key == "")
			}
		}
	}
}
