//go:build !windows

package app

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// failedDirectoryFlush makes the flush of an open directory fail with err, as a file system does that cannot flush a directory or has lost
// the write.
func failedDirectoryFlush(t *testing.T, err error) {
	t.Helper()
	previous := syncOpenDirectory
	syncOpenDirectory = func(file *os.File) error {
		return &os.PathError{Op: "sync", Path: file.Name(), Err: err}
	}
	t.Cleanup(func() { syncOpenDirectory = previous })
}

// The flush of a directory that fails for a real reason (EIO: the write is lost with a power cut) is reported, a directory that is not there
// is an error, and a file system that cannot flush a directory at all (EINVAL, ENOTSUP, EOPNOTSUPP, ENOSYS) has nothing more to give and is not.
func TestFlushDirectoryReportsARealFailureAndToleratesAnUnsupportedFlush(t *testing.T) {
	dir := t.TempDir()
	if err := flushDirectory(dir); err != nil {
		t.Fatalf("flushDirectory(%s) = %v, want nil", dir, err)
	}
	if err := flushDirectory(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("flushDirectory of a missing directory = %v, want not-exist", err)
	}
	for _, real := range []error{syscall.EIO, syscall.ENOSPC, syscall.EROFS} {
		failedDirectoryFlush(t, real)
		if err := flushDirectory(dir); !errors.Is(err, real) {
			t.Fatalf("flushDirectory where the flush fails (%v) = %v, want that error", real, err)
		}
	}
	for _, unsupported := range []error{syscall.EINVAL, syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.ENOSYS} {
		failedDirectoryFlush(t, unsupported)
		if err := flushDirectory(dir); err != nil {
			t.Fatalf("flushDirectory where the file system cannot flush a directory (%v) = %v, want nil", unsupported, err)
		}
	}
}

// A directory the process may search and write but not read is not opened for the flush, and that is not an error: its entries were just changed.
func TestFlushDirectoryToleratesADirectoryThatCannotBeOpened(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens every directory")
	}
	dir := filepath.Join(t.TempDir(), "writeonly")
	if err := os.Mkdir(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := flushDirectory(dir); err != nil {
		t.Fatalf("flushDirectory of a directory that cannot be read = %v, want nil", err)
	}
}
