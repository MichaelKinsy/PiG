package harness

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.RunWithoutGoroutineLeaks(m, endCrashedProcesses))
}
