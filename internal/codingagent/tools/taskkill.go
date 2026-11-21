package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

func taskkillExePath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}

// newTaskkillCommand selects the System32 executable independently of PATH. Upstream joins process.env.SystemRoot ||
// "C:\\Windows", so an unset or empty SystemRoot takes the default.
// Ports packages/coding-agent/src/utils/shell.ts.
func newTaskkillCommand(pid int) *exec.Cmd {
	//nolint:gosec // G204 (class a): program is System32\taskkill.exe under SystemRoot (default C:\Windows), as upstream; args are fixed /F /T /PID plus the pid of the bash child PiG started.
	return exec.Command(taskkillExePath(), "/F", "/T", "/PID", strconv.Itoa(pid))
}

// newTaskkillCommandContext builds one System32 taskkill command for all pids,
// bounded by ctx, as upstream killTrackedDetachedChildren runs on win32
// (Pi 31b407dc63, #11889).
func newTaskkillCommandContext(ctx context.Context, pids ...int) *exec.Cmd {
	args := make([]string, 0, 2+2*len(pids))
	args = append(args, "/F", "/T")
	for _, pid := range pids {
		args = append(args, "/PID", strconv.Itoa(pid))
	}
	//nolint:gosec // G204 (class a): program is System32\taskkill.exe under SystemRoot (default C:\Windows), as upstream; args are fixed /F /T plus /PID pairs for bash children PiG started.
	return exec.CommandContext(ctx, taskkillExePath(), args...)
}
// runTaskkill consumes spawn failures as Pi's error listener and catch do. The
// Go executor returns these failures instead of emitting an unhandled event.
func runTaskkill(command *exec.Cmd, run func(*exec.Cmd) error) {
	_ = run(command)
}
