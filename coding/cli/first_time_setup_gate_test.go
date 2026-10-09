package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// PiG's first-time setup gate (D88): only an interactive start without settings.json shows it; print, JSON and RPC modes
// never do, with or without a terminal.
func TestShouldRunFirstTimeSetupGate(t *testing.T) {
	fresh, existing := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(existing, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		mode       appMode
		configured bool
		agentDir   string
		want       bool
	}{
		{"interactive without settings.json", resolveAppMode("", false, true, true), false, fresh, true},
		{"interactive with settings.json", resolveAppMode("", false, true, true), false, existing, false},
		// Pi's gate returns false when ENV_AGENT_DIR is set (startup-ui.ts:144-146), which is how its tests start.
		{"interactive with PIG_CODING_AGENT_DIR", resolveAppMode("", false, true, true), true, fresh, false},
		{"print", resolveAppMode("", true, true, true), false, fresh, false},
		{"piped stdin", resolveAppMode("", false, false, true), false, fresh, false},
		{"json", resolveAppMode("json", false, true, true), false, fresh, false},
		{"rpc", resolveAppMode("rpc", false, true, true), false, fresh, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRunFirstTimeSetup(tc.mode, tc.configured, tc.agentDir); got != tc.want {
				t.Fatalf("shouldRunFirstTimeSetup = %v, want %v", got, tc.want)
			}
		})
	}
}
