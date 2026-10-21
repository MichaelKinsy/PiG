//go:build windows

package extensionconformance

import "os"

// processGone reports whether no process has the pid: opening a process that ended fails.
func processGone(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	_ = process.Release()
	return false
}

func killProcess(pid int) {
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
}
