//go:build !windows

package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// failedDirectorySync makes the flush of the directory fail with err, as a file system does that cannot flush a directory or has lost the write.
func failedDirectorySync(t *testing.T, err error) {
	t.Helper()
	previous := syncFile
	syncFile = func(file *os.File) error {
		return &os.PathError{Op: "sync", Path: file.Name(), Err: err}
	}
	t.Cleanup(func() { syncFile = previous })
}

// A flush of the directory that fails for a real reason (EIO: the write is lost with a power cut) is reported: the credential was not made durable and
// the caller must not be told it was. The file itself is replaced.
func TestWriteFileReportsADirectorySyncFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	failedDirectorySync(t, syscall.EIO)
	err := WriteFile(path, []byte("new"), 0o600)
	if !errors.Is(err, syscall.EIO) {
		t.Fatalf("WriteFile where the directory cannot be flushed (EIO) = %v, want an error that is EIO", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new" {
		t.Fatalf("content = %q, want the replacement in place", data)
	}
	noTemporaryFile(t, dir)
}

// A file system that cannot flush a directory at all (EINVAL: fsync on a special file, ENOTSUP and EOPNOTSUPP: no such operation, ENOSYS: not
// implemented, as some network and FUSE file systems answer) has nothing more to give: the write is complete and is not an error.
func TestWriteFileToleratesAFileSystemThatCannotSyncADirectory(t *testing.T) {
	for _, unsupported := range []error{syscall.EINVAL, syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.ENOSYS} {
		t.Run(unsupported.Error(), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "auth.json")
			if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			failedDirectorySync(t, unsupported)
			if err := WriteFile(path, []byte("new"), 0o600); err != nil {
				t.Fatalf("WriteFile where the directory cannot be flushed (%v) = %v, want nil", unsupported, err)
			}
			if data, _ := os.ReadFile(path); string(data) != "new" {
				t.Fatalf("content = %q, want the replacement", data)
			}
		})
	}
}
