package testenv

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// DeletedWorkingDirectory makes the process working directory unreadable, as libuv reports for a removed directory: os.Getwd fails with a not-exist error and Node's process.cwd() throws ENOENT. It restores the previous directory when the test ends. The test must not run in parallel.
func DeletedWorkingDirectory(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to remove a process's working directory")
	}
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Getwd(); err == nil {
		// macOS getcwd(3) still returns a removed directory's path, so Node's
		// process.cwd() (libuv uv_cwd) succeeds there and Pi raises nothing.
		t.Skip("this platform's getcwd still reports a removed working directory")
	}
}
