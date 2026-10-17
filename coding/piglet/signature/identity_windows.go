//go:build windows

package signature

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileBasicInfo is FILE_BASIC_INFO, which carries the change time that BY_HANDLE_FILE_INFORMATION lacks.
type fileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              uint32
}

// identify reads the volume serial number, file index, size, last write time, and change time of an opened file. NTFS updates the change time on every write and attribute change, including a restored last write time.
func identify(file *os.File, _ os.FileInfo) (fileIdentity, error) {
	handle := windows.Handle(file.Fd())
	var data windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &data); err != nil {
		return fileIdentity{}, &os.PathError{Op: "GetFileInformationByHandle", Path: file.Name(), Err: err}
	}
	var basic fileBasicInfo
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil { //nolint:gosec // G103: the API fills the FILE_BASIC_INFO layout fileBasicInfo mirrors, and x/sys takes that buffer as *byte.
		return fileIdentity{}, &os.PathError{Op: "GetFileInformationByHandleEx", Path: file.Name(), Err: err}
	}
	return fileIdentity{
		Device:   uint64(data.VolumeSerialNumber),
		Inode:    uint64(data.FileIndexHigh)<<32 | uint64(data.FileIndexLow),
		Size:     int64(data.FileSizeHigh)<<32 | int64(data.FileSizeLow),
		Modified: basic.LastWriteTime,
		Changed:  basic.ChangeTime,
	}, nil
}
