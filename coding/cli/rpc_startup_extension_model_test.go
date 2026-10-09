package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// Pi main.ts:736 loads extensions in createAgentSessionServices before main.ts:801 buildSessionOptions resolves --model and --models against that model runtime, and runRpcMode (main.ts:932) receives the same runtime. A -e provider therefore selects the RPC startup model, and each scope diagnostic is reported once.
func TestRPCStartupModelFromExtensionProvider(t *testing.T) {
	t.Parallel()
	fixture, err := filepath.Abs(filepath.Join("testdata", "startup-provider.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	const unmatched = `Warning: No models match pattern "zzz-no-match"`
	for _, tc := range []struct {
		name      string
		args      []string
		unmatched int
	}{
		{"cli model", []string{"--model", "startup-prov/startup-model"}, 0},
		{"scoped models", []string{"--models", "zzz-no-match,startup-prov/startup-model"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			env := []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_OFFLINE=1"}
			p := startRPCProcessAt(t, t.TempDir(), env, append([]string{"--no-extensions", "-e", fixture, "--no-session"}, tc.args...)...)
			p.send(`{"id":"state","type":"get_state"}`)
			p.await("extension model state", func(record rpcRecord) bool {
				if record["type"] != "response" || record["id"] != "state" {
					return false
				}
				data, _ := record["data"].(map[string]any)
				model, _ := data["model"].(map[string]any)
				if model["provider"] != "startup-prov" || model["id"] != "startup-model" {
					t.Errorf("startup model = %v", model)
				}
				return true
			})
			p.closeAndWait("after extension model state")
			if got := strings.Count(p.stderr.String(), unmatched); got != tc.unmatched {
				t.Errorf("%q reported %d times, want %d\n%s", unmatched, got, tc.unmatched, p.stderr.String())
			}
		})
	}
}
