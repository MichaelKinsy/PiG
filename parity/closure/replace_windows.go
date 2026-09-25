//go:build windows

package closure

import "golang.org/x/sys/windows"

func publishStore(from, to string) error {
	fromUTF16, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toUTF16, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromUTF16, toUTF16, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
