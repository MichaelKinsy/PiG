package tui

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Guards the TestMain temporary-directory scope: an extension host started by this package's tests must create its socket directory (pig-<uid>) inside the scoped directory that TestMain removes, not in the shared temporary directory.
func TestTempDirIsScopedToThePackageRun(t *testing.T) {
	testenv.RequireScopedTempDir(t, "pig-tui-")
	if got, want := os.Getenv("XDG_RUNTIME_DIR"), os.TempDir(); got != want {
		t.Fatalf("XDG_RUNTIME_DIR = %q, want the scoped temporary directory %q", got, want)
	}
}
