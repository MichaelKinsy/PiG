//go:build windows

package nodeerrno

import "os"

// Rename is os.Rename, which replaces an existing file as libuv's rename does on Windows.
func Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
