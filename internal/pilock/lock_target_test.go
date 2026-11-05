package pilock

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
