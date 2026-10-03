package tools

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Guards the TestMain temporary-directory scope: the bash tool's truncated-output logs must land in the directory TestMain removes.
func TestTempDirIsScopedToThePackageRun(t *testing.T) {
	testenv.RequireScopedTempDir(t, "pig-tools-")
}
