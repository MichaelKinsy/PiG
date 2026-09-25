package codingagent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// checkTmuxKeyboardSetup mirrors upstream checkTmuxKeyboardSetup
// (interactive-mode.ts:768-818). Returns a warning message or "".
func checkTmuxKeyboardSetup() string {
	if os.Getenv("TMUX") == "" {
		return ""
	}

	extKeys := tmuxShowOption("extended-keys")
	extKeysFormat := tmuxShowOption("extended-keys-format")

	// If we couldn't query tmux (timeout, sandbox, etc.), don't warn.
	if extKeys == "" {
		return ""
	}

	if extKeys != "on" && extKeys != "always" {
		return "tmux extended-keys is off. Modified Enter keys may not work. " +
			"Add `set -g extended-keys on` to ~/.tmux.conf and restart tmux."
	}

	if extKeysFormat == "xterm" {
		return "tmux extended-keys-format is xterm. Pi works best with csi-u. " +
			"Add `set -g extended-keys-format csi-u` to ~/.tmux.conf and restart tmux."
	}

	return ""
}

// tmuxShowOption runs `tmux show -gv <option>` with a 2s timeout.
func tmuxShowOption(option string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second) // upstream: coding-agent/src/modes/interactive/interactive-mode.ts:runTmuxShow
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "show", "-gv", option).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
