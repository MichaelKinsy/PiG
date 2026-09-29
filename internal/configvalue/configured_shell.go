package configvalue

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
)

// Ports packages/coding-agent/src/core/resolve-config-value.ts.

var getShellConfig = shellconfig.Default

// runShellCommandWithConfiguredShell uses the configured shell and falls back to the platform's default shell only if it could not start one.
func runShellCommandWithConfiguredShell(ctx context.Context, payload string) (string, bool) {
	if value, executed := runConfiguredShell(ctx, payload); executed {
		return value, value != ""
	}
	return runDefaultShell(ctx, payload)
}

// runConfiguredShell mirrors executeWithConfiguredShell. A failed command is an executed command, so it must not cause a fallback that executes it again.
func runConfiguredShell(ctx context.Context, payload string) (value string, executed bool) {
	shell, err := getShellConfig()
	if err != nil {
		return "", false
	}
	args := shell.Args
	if shell.CommandTransport != "stdin" {
		args = append(append([]string{}, args...), payload)
	}
	cmd := configuredShellCommand(ctx, shell.Path, shell.CommandTransport == "stdin", args...)
	if shell.CommandTransport == "stdin" {
		cmd.Stdin = strings.NewReader(payload)
	}
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return "", false
		}
		return "", true
	}
	return strings.TrimSpace(string(out)), true
}
