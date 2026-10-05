//go:build windows

package node

import (
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// openReadOnly opens path for reading like libuv: it shares read, write and delete access, so the file can be renamed
// or removed while open, and it opens directories, which the caller then refuses. Windows has no O_NOFOLLOW, so the
// caller checks the final component itself.
func openReadOnly(path string, _ bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}
