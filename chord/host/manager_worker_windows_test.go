//go:build windows

package host

import (
	"io"

	"golang.org/x/sys/windows"
)

// workerPipe is the pipe the relay (testdata/manager_relay.mjs) gave the worker on CRT descriptor 3: a plain pipe
// (libuv's "pipe" stdio), no IPC frames.
func workerPipe() (io.ReadWriteCloser, error) { return adoptPipe(3) }

// alive is manager.test.ts alive(pid): process.kill(pid, 0), which on Windows opens the process and asks whether it
// has ended.
func alive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == 259 // STILL_ACTIVE
}
