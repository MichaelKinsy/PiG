//go:build !windows

package configvalue

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// defaultShell is the shell Node's execSync runs: /bin/sh, or /system/bin/sh
// where Node reports process.platform "android" (Termux has no /bin).
func defaultShell(goos string) string {
	if goos == "android" {
		return "/system/bin/sh"
	}
	return "/bin/sh"
}

// runShellCommand executes payload via the default shell (defaultShell) with
// -c, returns trimmed stdout. Matches upstream execSync default-shell behavior
// on Unix. stderr is discarded.
// On non-zero exit or empty stdout, returns ("", false).
func runShellCommand(ctx context.Context, payload string) (string, bool) {
	return runDefaultShell(ctx, payload)
}

func configuredShellCommand(ctx context.Context, path string, _ bool, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, path, args...)
}

func runDefaultShell(ctx context.Context, payload string) (string, bool) {
	cmd := exec.CommandContext(ctx, defaultShell(runtime.GOOS), "-c", payload)
	cmd.Stdin = nil
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", false
		}
		return "", false
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", false
	}
	return v, true
}
