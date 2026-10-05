//go:build windows

package installchange

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// identityOf opens path for attributes only and reads its volume serial number and file index with
// GetFileInformationByHandle, so the check neither locks nor changes the file.
func identityOf(path string) (Identity, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Identity{}, err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return Identity{}, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return Identity{}, &os.PathError{Op: "GetFileInformationByHandle", Path: path, Err: err}
	}
	return Identity{
		Path:    path,
		File:    FileID{Device: uint64(information.VolumeSerialNumber), Index: uint64(information.FileIndexHigh)<<32 | uint64(information.FileIndexLow)},
		Size:    int64(information.FileSizeHigh)<<32 | int64(information.FileSizeLow),
		ModTime: time.Unix(0, information.LastWriteTime.Nanoseconds()),
	}, nil
}
