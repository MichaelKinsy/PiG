//go:build !windows

package crossspawn

import (
	"context"
	"os/exec"
)

func command(ctx context.Context, name string, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
