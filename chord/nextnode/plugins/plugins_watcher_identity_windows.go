//go:build windows

package plugins

// Ports packages/chord/src/node/plugins/watcher.ts

import "golang.org/x/sys/windows"

// statIdentity is the file index and the creation time of the entry at path, not following a link: what libuv gives Node's `ino`
// and `birthtimeNs` there.
func statIdentity(path string) (ino uint64, birth int64, ok bool) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	// Backup semantics open a directory; no access is asked, only its attributes read.
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, 0, false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &info) != nil {
		return 0, 0, false
	}
	return uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), info.CreationTime.Nanoseconds(), true
}
