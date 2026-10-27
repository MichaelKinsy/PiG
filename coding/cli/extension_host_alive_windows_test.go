package cli

import (
	"errors"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

// extensionProcessAlive reports whether pid names a running process: a process that was terminated but whose handles are still being released still has the exit code STILL_ACTIVE until it is gone.
func extensionProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	if windows.GetExitCodeProcess(handle, &code) != nil {
		return true
	}
	return code == windowsStillActive
}
