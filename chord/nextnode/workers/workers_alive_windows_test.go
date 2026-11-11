//go:build windows

package workers

import "golang.org/x/sys/windows"

// alive reports whether process pid lives.
func alive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	return windows.GetExitCodeProcess(handle, &code) == nil && code == 259
}
