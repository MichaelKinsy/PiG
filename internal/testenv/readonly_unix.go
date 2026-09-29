//go:build !windows

package testenv

import (
	"os"
	"testing"
)

// ReadOnlyDir lets the current user list and traverse dir but not add or
// remove its entries (mode 0555) until the test ends. The test must start with
// RunUnprivileged, because root ignores permission bits.
func ReadOnlyDir(t testing.TB, dir string) {
	t.Helper()
	requireUnprivileged(t)
	if os.Geteuid() == 0 {
		t.Fatal("root bypasses file permission bits")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("restore %s: %v", dir, err)
		}
	})
}
