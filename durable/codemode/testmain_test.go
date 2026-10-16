package codemode

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory to one directory the run removes. A code mode run keeps its pi-codemode-* output
// directory in the OS temporary directory after it ends, as Pi does, so these tests would otherwise leave them in the shared one
// (CI test-fast assert-clean-tmp).
func TestMain(m *testing.M) {
	os.Exit(testenv.RunScoped(m, "pig-durable-codemode-"))
}

// Guards the TestMain wiring: without it the run output directories land in the temporary directory the environment names.
func TestOutputDirectoriesStayInThePackageRun(t *testing.T) {
	testenv.RequireScopedTempDir(t, "pig-durable-codemode-")
}
