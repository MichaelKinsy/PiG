//go:build !windows

package crossspawn

import (
	"context"
	"os/exec"
)

func command(ctx context.Context, _, name string, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: the program and arguments come from the user, the project config, or PiG's own toolchain lookup; the CLI runs as the user with no sandbox and launches them as Pi's child_process.spawn does
}
