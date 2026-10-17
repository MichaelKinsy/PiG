//go:build windows

package daemon

import "syscall"

// hiddenAttribute is whether the file has Windows' hidden attribute (winapi_util::file::is_hidden).
func hiddenAttribute(path string) bool {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	attributes, err := syscall.GetFileAttributes(name)
	return err == nil && attributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
}
