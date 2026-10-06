//go:build windows

package codingagent

import (
	"errors"

	"golang.org/x/sys/windows"
)

const stillActive = 259

// processAlive reports whether pid names a running process. An access-denied open means it exists under another user.
func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	if windows.GetExitCodeProcess(handle, &code) != nil {
		return true
	}
	return code == stillActive
}
