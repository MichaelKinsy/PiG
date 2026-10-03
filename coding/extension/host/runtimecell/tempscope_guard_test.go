package runtimecell

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Guards the TestMain temporary-directory scope: this package's tests must not write scratch files or directories into the shared temporary directory, where a killed or forgetful test leaves them for good.
func TestTempDirIsScopedToThePackageRun(t *testing.T) {
	testenv.RequireScopedTempDir(t, "pig-cell-")
}
