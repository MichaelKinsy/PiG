package tools

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain lets the test binary stand in for the shell and report how it was
// started or what environment it received. It scopes the temporary directory to
// this run: the bash tool keeps a pi-bash-*.log for output it truncates.
func TestMain(m *testing.M) {
	reportShellEnvIfRequested()
	testenv.ReportStartupIfRequested()
	testenv.ReportEnvironIfRequested()
	os.Exit(testenv.RunScoped(m, "pig-tools-"))
}
