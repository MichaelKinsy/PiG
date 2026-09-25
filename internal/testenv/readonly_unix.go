//go:build !windows

package testenv

import (
	"os"
	"testing"
)

// ReadOnlyDir lets the current user list and traverse dir but not add or
// remove its entries (mode 0555) until the test ends. Root ignores permission
// bits, so the test is skipped there.
func ReadOnlyDir(t testing.TB, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permission bits")
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
