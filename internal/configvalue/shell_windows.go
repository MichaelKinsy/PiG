//go:build windows

package configvalue

import (
	"context"
	"os/exec"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/crossspawn"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// runShellCommand mirrors executeCommandUncached on win32: use the configured bash before falling back to the default shell if bash could not start.
func runShellCommand(ctx context.Context, payload string) (string, bool) {
	return runShellCommandWithConfiguredShell(ctx, payload)
}

// configuredShellCommand starts the configured bash as upstream's spawnSync does: stdio is ["pipe" when the command comes from stdin, else "ignore", "pipe", "ignore"] and windowsHide is true.
func configuredShellCommand(ctx context.Context, path string, commandFromStdin bool, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	stdin := nodespawn.Ignore
	if commandFromStdin {
		stdin = nodespawn.Pipe
	}
	nodespawn.HideWindow(cmd, stdin, nodespawn.Pipe, nodespawn.Ignore)
	// Upstream's spawnSync gives bash Node's libuv command line, which Git
	// Bash parses with MSYS2 rules, not the C runtime's.
	nodespawn.SetCommandLine(cmd)
	return cmd
}

// runDefaultShell mirrors upstream executeWithDefaultShell: Node's execSync
// runs %ComSpec% /d /s /c "<command>" for cmd, or -c for a non-cmd ComSpec.
func runDefaultShell(ctx context.Context, payload string) (string, bool) {
	cmd := crossspawn.ShellCommand(ctx, payload)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(out))
	return value, value != ""
}
