//go:build !windows

package crossspawn

import (
	"context"
	"os/exec"
)

func command(ctx context.Context, _, name string, args []string) *exec.Cmd {
	//nolint:gosec // G204 (class c): generic spawn wrapper: the callers pass the user's npmCommand and git settings, an MCP server's configured command, or PiG's own self-update tools, as Pi's cross-spawn does.
	return exec.CommandContext(ctx, name, args...)
}
