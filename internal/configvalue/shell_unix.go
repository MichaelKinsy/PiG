//go:build !windows

package configvalue

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// runShellCommand executes payload via /bin/sh -c, returns trimmed
// stdout. Matches upstream execSync default-shell behavior on Unix
// (Node's execSync uses /bin/sh -c on POSIX). stderr is discarded.
// On non-zero exit or empty stdout, returns ("", false).
func runShellCommand(ctx context.Context, payload string) (string, bool) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", payload)
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
