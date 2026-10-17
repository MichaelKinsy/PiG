package llama

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain fails the package when a test leaves a goroutine blocked, such as a view flow whose list never settled.
func TestMain(m *testing.M) {
	os.Exit(testenv.RunWithoutGoroutineLeaks(m))
}
