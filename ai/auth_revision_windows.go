//go:build windows

package ai

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileBasicInfo is FILE_BASIC_INFO, which carries the change time that
// BY_HANDLE_FILE_INFORMATION lacks.
type fileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              uint32
}

// authFileRevision identifies one on-disk version of auth.json. Mirrors
// upstream utils/paths.ts getFileRevision, whose Node stat on Windows reports
// the volume serial number as dev and the file index as ino: volume, file
// index, size, and the modification and change times. An atomic replacement
// therefore changes the revision even when it preserves size and mtime.
func authFileRevision(path string) (string, bool) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var identity windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &identity); err != nil {
		return "", false
	}
	var basic fileBasicInfo
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil { //nolint:gosec // G103: the API fills the FILE_BASIC_INFO layout fileBasicInfo mirrors, and x/sys takes that buffer as *byte.
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d:%d:%d:%d",
		identity.VolumeSerialNumber,
		uint64(identity.FileIndexHigh)<<32|uint64(identity.FileIndexLow),
		uint64(identity.FileSizeHigh)<<32|uint64(identity.FileSizeLow),
		basic.LastWriteTime, basic.ChangeTime, basic.CreationTime), true
}
