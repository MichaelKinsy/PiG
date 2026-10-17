//go:build !windows

package codingagent

import "os"

// terminateSelf is what `kill -9 $$` does in the .sh editor: the process dies of SIGKILL, so its parent sees no exit status.
func terminateSelf() {
	if process, err := os.FindProcess(os.Getpid()); err == nil {
		_ = process.Kill()
	}
}
