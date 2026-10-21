package shellconfig

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodefs"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// findTimeout is the time `which` or `where` gets, upstream's spawnSync timeout.
const findTimeout = 5 * time.Second

// FindExecutableOnPath asks the platform lookup command for the first match of executable: `where` on Windows, where
// the match must also pass the access check ([nodefs.Access]) because `where` can report a path that does not exist, and
// `which` elsewhere, whose output is trusted so Termux and special file systems work. It returns "" when the command
// fails, times out, or finds nothing.
//
// Ports packages/coding-agent/src/utils/shell.ts (findExecutableOnPath).
func FindExecutableOnPath(executable string) string {
	windows := runtime.GOOS == "windows"
	name := "which"
	if windows {
		name = "where"
	}
	ctx, cancel := context.WithTimeout(context.Background(), findTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, executable)
	// Upstream's spawnSync pipes stdout and stderr, keeps stdin closed, and hides the Windows console window.
	nodespawn.HideWindow(command, nodespawn.Ignore, nodespawn.Pipe, nodespawn.Pipe)
	nodespawn.SetProgram(command)
	output, err := command.Output()
	if err != nil || len(output) == 0 {
		return ""
	}
	return FirstMatch(string(output), windows, nodefs.Access)
}

// FirstMatch is the first line of the lookup command's output. On Windows the path must also exist according to exists,
// which upstream checks with accessSync(F_OK), not existsSync, so a runnable Microsoft Store alias is found.
func FirstMatch(output string, windows bool, exists func(string) bool) string {
	first, _, _ := strings.Cut(strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n")), "\n")
	if first == "" {
		return ""
	}
	if windows && !exists(first) {
		return ""
	}
	return first
}
