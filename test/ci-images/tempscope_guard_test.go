package ciimages

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Guards the TestMain temporary-directory scope: the pig binary these tests run must create its extension-host socket directory (pig-<uid>) inside the scoped directory that TestMain removes, not in the shared temporary directory.
func TestTempDirIsScopedToThePackageRun(t *testing.T) {
	testenv.RequireScopedTempDir(t, "pig-ci-images-")
}
