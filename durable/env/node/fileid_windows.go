//go:build windows

package node

import (
	"io/fs"

	"golang.org/x/sys/windows"
)

// fileIdentity is the volume serial number and file index of a path, as Node's stat reports dev and ino. It opens the
// path without following a final symbolic link unless follow is set, as libuv's lstat and stat do.
func fileIdentity(path string, _ fs.FileInfo, follow bool) (dev, ino uint64) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	attributes := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS)
	if !follow {
		attributes |= windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, attributes, 0)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return 0, 0
	}
	return uint64(information.VolumeSerialNumber), uint64(information.FileIndexHigh)<<32 | uint64(information.FileIndexLow)
}
