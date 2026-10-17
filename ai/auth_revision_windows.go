//go:build windows

package ai

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/profiling"
)

// fileRevision is the identity authFileRevision reads. It is comparable, so a read that finds the file unchanged allocates nothing; the zero value is "no revision read".
type fileRevision struct {
	volume                     uint32
	index, size                uint64
	modified, changed, created int64
}

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
func authFileRevision(path string) (fileRevision, bool) {
	profiling.FileRevisionStats.Add(1)
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fileRevision{}, false
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fileRevision{}, false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var identity windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &identity); err != nil {
		return fileRevision{}, false
	}
	var basic fileBasicInfo
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil { //nolint:gosec // G103: the API fills the FILE_BASIC_INFO layout fileBasicInfo mirrors, and x/sys takes that buffer as *byte.
		return fileRevision{}, false
	}
	return fileRevision{
		volume:   identity.VolumeSerialNumber,
		index:    uint64(identity.FileIndexHigh)<<32 | uint64(identity.FileIndexLow),
		size:     uint64(identity.FileSizeHigh)<<32 | uint64(identity.FileSizeLow),
		modified: basic.LastWriteTime, changed: basic.ChangeTime, created: basic.CreationTime,
	}, true
}
