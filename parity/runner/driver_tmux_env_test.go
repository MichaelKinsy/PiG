//go:build parity

package runner

import (
	"slices"
	"strings"
	"testing"
)

func TestTmuxArgsUseProcessIsolatedServer(t *testing.T) {
	args := tmuxArgs("capture-pane", "-p")
	if len(args) != 4 || args[0] != "-L" || args[1] != tmuxSocketName || !slices.Equal(args[2:], []string{"capture-pane", "-p"}) {
		t.Fatalf("tmux args = %q, want isolated socket %q before command", args, tmuxSocketName)
	}
	if want := "pig-parity-" + parityRunID(); tmuxSocketName != want {
		t.Fatalf("tmux socket = %q, want %q", tmuxSocketName, want)
	}
}

func TestTmuxEnvPrefixClearsAmbientIntermediaryCapabilities(t *testing.T) {
	prefix := tmuxEnvPrefix("")
	for _, name := range []string{"HERDR_ENV", "HERDR_KITTY_GRAPHICS", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "HERDR_TAB_ID", "HERDR_WORKSPACE_ID"} {
		if !strings.Contains(prefix, "unset "+name) && !strings.Contains(prefix, " "+name) {
			t.Fatalf("tmux environment prefix does not clear %s: %q", name, prefix)
		}
	}
	if !strings.Contains(prefix, "export COLORTERM=truecolor") {
		t.Fatalf("tmux environment prefix lost deterministic color mode: %q", prefix)
	}
}
