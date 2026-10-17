package codemode

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory to one directory the run removes. codemode saves output too large for a reply to
// pi-codemode-*.txt/.png files in the OS temporary directory, as Pi does, so these tests would otherwise leave them in the
// shared one (CI test-fast assert-clean-tmp).
func TestMain(m *testing.M) {
	os.Exit(testenv.RunScoped(m, "pig-builtin-codemode-"))
}

// Guards the TestMain wiring: without it the saved output files land in the temporary directory the environment names.
func TestOutputFilesStayInThePackageRun(t *testing.T) {
	testenv.RequireScopedTempDir(t, "pig-builtin-codemode-")
}
