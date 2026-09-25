package experimental

import (
	"os"
	"testing"
)

// socketDir returns a short temporary directory for Unix socket paths, which
// must fit sun_path: at most 107 bytes before the terminator on Windows and
// Linux, 103 on macOS. t.TempDir adds the test's name, and on Windows it lives
// under the user's profile, so a long test name alone overflows it.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
