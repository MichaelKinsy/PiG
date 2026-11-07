package node

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain lets the test binary stand in for the shell and report how it was started, what environment it received or which
// session it runs in. It scopes the temporary directory to this run: command output that overflows is kept in a
// pi-output-*.log file.
func TestMain(m *testing.M) {
	testenv.ReportStartupIfRequested()
	testenv.ReportEnvironIfRequested()
	testenv.ReportSessionIfRequested()
	os.Exit(testenv.RunScoped(m, "pig-env-"))
}
