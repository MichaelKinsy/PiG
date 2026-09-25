//go:build windows

package codingagent

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Directory access rights: FILE_ADD_FILE and FILE_ADD_SUBDIRECTORY share
// their values with FILE_WRITE_DATA and FILE_APPEND_DATA.
const (
	fileAddFile         = windows.FILE_WRITE_DATA
	fileAddSubdirectory = windows.FILE_APPEND_DATA
)

// replacementDirectoryWritable is the Windows form of access(dir, W_OK|X_OK):
// opening the executable's directory for adding entries and traversal runs
// the DACL access check without changing the directory.
func replacementDirectoryWritable(path string) bool {
	dir, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return false
	}
	handle, err := windows.CreateFile(dir,
		fileAddFile|fileAddSubdirectory|windows.FILE_TRAVERSE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(handle)
	return true
}
