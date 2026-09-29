package codingagent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// checkTmuxKeyboardSetup mirrors Pi's concurrent, best-effort keyboard check
// (packages/coding-agent/src/modes/interactive/interactive-mode.ts:1214-1255).
func checkTmuxKeyboardSetup() string {
	if os.Getenv("TMUX") == "" {
		return ""
	}

	return tmuxKeyboardSetup(tmuxShowOption)
}

func tmuxKeyboardSetup(query func(string) string) string {
	var extKeys, extKeysFormat string
	var queries sync.WaitGroup
	queries.Go(func() { extKeys = query("extended-keys") })
	queries.Go(func() { extKeysFormat = query("extended-keys-format") })
	queries.Wait()

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
	cmd := exec.CommandContext(ctx, "tmux", "show", "-gv", option)
	// Upstream's spawn finds tmux with libuv's search on Windows.
	nodespawn.SetProgram(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
