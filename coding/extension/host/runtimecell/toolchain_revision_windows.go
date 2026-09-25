//go:build windows

package runtimecell

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type toolchainFileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              uint32
}

func toolchainFileRevision(path string) (string, bool) {
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
	var basic toolchainFileBasicInfo
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil { //nolint:gosec // G103: the API fills the FILE_BASIC_INFO layout toolchainFileBasicInfo mirrors, and x/sys takes that buffer as *byte.
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d:%d:%d:%d",
		identity.VolumeSerialNumber,
		uint64(identity.FileIndexHigh)<<32|uint64(identity.FileIndexLow),
		uint64(identity.FileSizeHigh)<<32|uint64(identity.FileSizeLow),
		basic.LastWriteTime, basic.ChangeTime, basic.CreationTime), true
}
