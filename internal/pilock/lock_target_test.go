package pilock

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// createLockTarget makes the file an auth lock guards, as the callers of Acquire and AcquireAuthSync do before they lock (Pi's ensureFileExists, auth-storage.ts:63-67): the lock follows the file's symlinks, so the file must exist (D236).
func createLockTarget(t testing.TB, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
}

// resolvedLockTarget is the path the auth locks guard for the file at path, which exists: proper-lockfile's default realpath (lib/lockfile.js resolveCanonicalPath) puts the lock directory beside fs.realpath(file), so an error from creating it names that directory, not the caller's spelling of the path (D236). A macOS temporary directory is /var/folders/..., a symlink to /private/var/folders/.... Windows keeps the case and the 8.3 short names of the path, which Go's EvalSymlinks would expand.
func resolvedLockTarget(t testing.TB, path string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
