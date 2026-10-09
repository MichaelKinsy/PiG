package shellconfig

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// findTimeout is the time `which` or `where` gets, upstream's spawnSync timeout.
const findTimeout = 5 * time.Second

// FindExecutableOnPath asks the platform lookup command for the first match of executable: `where` on Windows, where
// the match must also exist because `where` can report a path that does not, and `which` elsewhere, whose output is
// trusted so Termux and special file systems work. It returns "" when the command fails, times out, or finds nothing.
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
	firstMatch, _, _ := strings.Cut(strings.TrimSpace(strings.ReplaceAll(string(output), "\r\n", "\n")), "\n")
	if firstMatch == "" {
		return ""
	}
	if windows {
		if _, err := os.Stat(firstMatch); err != nil {
			return ""
		}
	}
	return firstMatch
}
