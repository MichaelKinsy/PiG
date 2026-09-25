// Package crossspawn starts a program the way upstream's spawnProcess does
// (coding-agent utils/child-process.ts): Node's spawn on Unix, and on Windows
// the cross-spawn package, which runs a command that is not a .exe or .com
// file -- such as npm.cmd -- through cmd.exe with every argument escaped for
// cmd.exe.
package crossspawn

import (
	"context"
	"os/exec"
)

// Command returns the command that runs name with args, as
// exec.CommandContext does, but able to start Windows command shims.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	return command(ctx, name, args)
}
