package tools

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory to this run: bash output that
// overflows is kept in a pi-output-*.log file.
func TestMain(m *testing.M) {
	os.Exit(testenv.RunScoped(m, "pig-durable-tools-"))
}
