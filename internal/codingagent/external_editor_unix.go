//go:build !windows

package codingagent

import (
	"context"
	"os/exec"
)

// editorCommand runs the editor directly, as upstream spawns it without a
// shell outside Windows.
func editorCommand(ctx context.Context, name string, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
