//go:build parity

package runner

import (
	"os"
	"os/exec"
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

// Pi checks both server options asynchronously (interactive-mode.ts:1214-1259). The harness owns the terminal configuration, not the relative arrival time of its warning.
func TestTmuxServerUsesPiKeyboardProtocol(t *testing.T) {
	tmuxServerMu.Lock()
	err := ensureTmuxServer()
	tmuxServerMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// A helper test process inherits the run ID but does not own this server. Its TestMain cleanup must leave the parent's keeper alive.
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	child.Env = append(os.Environ(), "PIG_PARITY_RUN_ID="+parityRunID())
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("helper exit: %v: %s", err, out)
	}
	options := []struct{ name, value string }{{"extended-keys", "on"}}
	if tmuxExtendedKeysFormatSupported {
		options = append(options, struct{ name, value string }{"extended-keys-format", "csi-u"})
	} else {
		t.Log("tmux has no extended-keys-format option (added in tmux 3.5); its single extended-key format applies")
	}
	for _, option := range options {
		out, err := exec.CommandContext(t.Context(), "tmux", tmuxArgs("show", "-gv", option.name)...).CombinedOutput()
		if err != nil {
			t.Fatalf("query %s: %v: %s", option.name, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != option.value {
			t.Errorf("%s = %q, want %q", option.name, got, option.value)
		}
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
