//go:build !windows

package pico3

import (
	"os"
	"syscall"
)

// signalName renders the terminating signal as Node does ("SIGKILL").
func signalName(state *os.ProcessState) string {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	names := map[syscall.Signal]string{syscall.SIGKILL: "SIGKILL", syscall.SIGTERM: "SIGTERM", syscall.SIGINT: "SIGINT", syscall.SIGHUP: "SIGHUP", syscall.SIGQUIT: "SIGQUIT", syscall.SIGSEGV: "SIGSEGV", syscall.SIGABRT: "SIGABRT", syscall.SIGPIPE: "SIGPIPE"}
	return names[status.Signal()]
}
