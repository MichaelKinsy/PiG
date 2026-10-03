// Package crossspawn starts a program the way upstream's spawnProcess does
// (coding-agent utils/child-process.ts): Node's spawn on Unix, and on Windows
// the cross-spawn package, which checks shebang interpreters before selecting
// direct execution or escaped cmd.exe execution for command shims.
package crossspawn

import (
	"context"
	"os/exec"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// Command returns the command that runs name with args in dir. It resolves
// Windows command shims and shebangs in the child's working directory before
// choosing shell escaping. An empty dir inherits the current directory. The
// child starts in the directory libuv gives Node's spawn for dir
// (nodespawn.SetDirectory).
func Command(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	cmd := command(ctx, dir, name, args)
	cmd.Dir = dir
	nodespawn.SetDirectory(cmd)
	// Android forbids execve of a file in the app data directory, so a start
	// there goes through the system linker. It does nothing elsewhere.
	linkerexec.Prepare(cmd)
	return cmd
}
