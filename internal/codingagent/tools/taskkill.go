package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// newTaskkillCommand selects the System32 executable independently of PATH.
// Ports packages/coding-agent/src/utils/shell.ts.
func newTaskkillCommand(pid int) *exec.Cmd {
	root, present := os.LookupEnv("SystemRoot")
	if !present {
		root = `C:\Windows`
	}
	return exec.Command(filepath.Join(root, "System32", "taskkill.exe"), "/F", "/T", "/PID", strconv.Itoa(pid))
}

// runTaskkill consumes spawn failures as Pi's error listener and catch do. The
// Go executor returns these failures instead of emitting an unhandled event.
func runTaskkill(command *exec.Cmd, run func(*exec.Cmd) error) {
	_ = run(command)
}
