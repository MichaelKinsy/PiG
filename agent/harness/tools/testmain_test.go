package tools

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory to this run: the execution environment keeps overflow output in tmp-*/pi-output-*.log.
func TestMain(m *testing.M) {
	os.Exit(testenv.RunScoped(m, "pig-htools-"))
}
