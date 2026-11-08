package store

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain fails the package's test run when any test leaks a goroutine (one blocked forever on a primitive no live
// goroutine can reach: a commit nobody waits for). The stalled sources of the import tests, which model a promise that
// never settles, end after the tests.
func TestMain(m *testing.M) {
	os.Exit(testenv.RunWithoutGoroutineLeaks(m, func() { close(stalledForever) }))
}
