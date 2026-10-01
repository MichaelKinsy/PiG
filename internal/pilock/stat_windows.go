package pilock

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// lstatLock reads the lock path itself and statLock follows a link, each with the fallback Node's fs.stat has on Windows. A lock directory that another process is removing stays delete-pending until its RemoveDirectory handle closes; opening it then fails with ERROR_ACCESS_DENIED, so os.Lstat and os.Stat fail. libuv (src/win/fs.c fs__stat_impl_from_path, in the libuv 1.51.0 that Node 22.19.0, Pi's minimum engine, bundles) answers ERROR_ACCESS_DENIED and ERROR_SHARING_VIOLATION from the entry in the parent directory instead (fs__stat_directory), so proper-lockfile sees the holder's fresh directory and reports ELOCKED, which Pi retries.
func lstatLock(path string) (fs.FileInfo, error) { return statWithEntryFallback(path, os.Lstat) }

func statLock(path string) (fs.FileInfo, error) { return statWithEntryFallback(path, os.Stat) }

func statWithEntryFallback(path string, stat func(string) (fs.FileInfo, error)) (fs.FileInfo, error) {
	info, err := stat(path)
	if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
		return info, err
	}
	name := filepath.Base(path)
	// FindFirstFile would expand wildcard characters. mkdir never reaches this branch, because CreateDirectory rejects such names first; libuv's fs__stat_directory rejects them with ERROR_INVALID_NAME.
	if strings.ContainsAny(name, `*?<>"`) {
		return nil, err
	}
	pathp, convErr := windows.UTF16PtrFromString(path)
	if convErr != nil {
		return nil, err
	}
	var data windows.Win32finddata
	handle, findErr := windows.FindFirstFile(pathp, &data)
	if findErr != nil {
		return nil, &fs.PathError{Op: "FindFirstFile", Path: path, Err: findErr}
	}
	_ = windows.FindClose(handle)
	// The entry is the path's own, not a link target's. fs.stat follows links and fs__stat_directory cannot, so a reparse point keeps the original error, including a link whose target is being removed.
	if data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, err
	}
	return &directoryEntryInfo{name: name, data: data}, nil
}

// directoryEntryInfo is the parent directory's entry for a name that cannot be opened.
type directoryEntryInfo struct {
	name string
	data windows.Win32finddata
}

func (i *directoryEntryInfo) Name() string { return i.name }
func (i *directoryEntryInfo) Size() int64 {
	return int64(i.data.FileSizeHigh)<<32 | int64(i.data.FileSizeLow)
}
func (i *directoryEntryInfo) Mode() fs.FileMode {
	mode := fs.FileMode(0o666)
	if i.data.FileAttributes&windows.FILE_ATTRIBUTE_READONLY != 0 {
		mode = 0o444
	}
	if i.IsDir() {
		mode |= fs.ModeDir | 0o111
	}
	return mode
}
func (i *directoryEntryInfo) ModTime() time.Time {
	return time.Unix(0, i.data.LastWriteTime.Nanoseconds())
}
func (i *directoryEntryInfo) IsDir() bool {
	return i.data.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
}
func (i *directoryEntryInfo) Sys() any { return &i.data }
