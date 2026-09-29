package pilock

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openLegacy(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// OPEN_REPARSE_POINT opens the link itself, not its target. DELETE access makes old handles without FILE_SHARE_DELETE report contention before we can remove their lock.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return nil, ErrLegacyLocked
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func lockLegacy(file *os.File) error {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return ErrLegacyLocked
	}
	return err
}

// observeLegacy records the sidecar's file ID now. os.SameFile may decide os.Lstat results by path name on Windows, so an observation taken with os.Lstat would match whichever file the name holds later.
func observeLegacy(path string, _ fs.FileInfo) (fs.FileInfo, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Attribute access takes no part in sharing checks, so it cannot block a v0.2.0 writer.
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	info, err := file.Stat()
	return info, errors.Join(err, file.Close())
}

func removeLegacy(file *os.File, path string) error {
	// Move the verified handle's file, not a possibly replaced pathname, off the lock name first. Deleting it in place would leave a delete-pending lock name until the handle closes, and other upgraders' stat, open and mkdir calls fail on that name with access denied.
	handle := windows.Handle(file.Fd())
	if err := renameLegacy(handle, fmt.Sprintf("%s.%d-%x.reclaimed", filepath.Base(path), os.Getpid(), rand.Uint64())); err != nil {
		return err
	}
	// Deletion completes when replaceLegacy closes the handle, releasing its byte-range lock.
	deleteFile := byte(1)
	return windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &deleteFile, uint32(unsafe.Sizeof(deleteFile)))
}

// renameLegacy renames the handle's file within its directory without replacing an existing name.
func renameLegacy(handle windows.Handle, name string) error {
	wide, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	// FILE_RENAME_INFO with one path component: without separators or a RootDirectory, the name stays in the file's directory.
	var info struct {
		replaceIfExists uint32
		rootDirectory   windows.Handle
		fileNameLength  uint32
		fileName        [windows.MAX_PATH]uint16
	}
	if copy(info.fileName[:], wide) < len(wide) {
		return fmt.Errorf("rename legacy lock: name too long: %s", name)
	}
	info.fileNameLength = uint32(len(wide)-1) * 2
	return windows.SetFileInformationByHandle(handle, windows.FileRenameInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))) //nolint:gosec // G103: the API reads the FILE_RENAME_INFO layout info mirrors, and x/sys takes that buffer as *byte.
}
