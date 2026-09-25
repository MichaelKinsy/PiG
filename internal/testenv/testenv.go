// Package testenv checks host capabilities a test needs, so a test runs
// wherever the host provides them and says why it skipped where the host
// does not. Only test code imports this package.
package testenv

import (
	"os"
	"testing"
)

// Symlink creates newname as a symbolic link to oldname. When the host does
// not let this process create symbolic links (Windows without Developer Mode
// or elevation), the test is skipped; any other error fails it.
func Symlink(t testing.TB, oldname, newname string) {
	t.Helper()
	err := os.Symlink(oldname, newname)
	if err == nil {
		return
	}
	if symlinkPrivilegeMissing(err) {
		t.Skipf("creating symbolic links needs Developer Mode or elevation on Windows: %v", err)
	}
	t.Fatalf("create symlink %s -> %s: %v", newname, oldname, err)
}
