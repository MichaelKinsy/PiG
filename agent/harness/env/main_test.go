package env

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain lets the test binary stand in for the shell and report how it was
// started or what environment it received.
func TestMain(m *testing.M) {
	testenv.ReportStartupIfRequested()
	testenv.ReportEnvironIfRequested()
	os.Exit(m.Run())
}
