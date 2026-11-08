package documents

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain fails the package's test run when any test leaks a goroutine (one blocked forever on a primitive no live
// goroutine can reach: a span nobody ended, a commit nobody waits for).
func TestMain(m *testing.M) { os.Exit(testenv.RunWithoutGoroutineLeaks(m)) }
