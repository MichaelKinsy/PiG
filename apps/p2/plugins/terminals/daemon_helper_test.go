package terminals

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
)

// useDaemon: see p2test.UseTerminalDaemon.
func useDaemon(t *testing.T) { t.Helper(); p2test.UseTerminalDaemon(t) }

// poll waits until check holds (Pi's `poll`: 50 ms steps, 10 s by default).
func poll(t testing.TB, check func() bool, timeout ...time.Duration) {
	t.Helper()
	limit := 10 * time.Second
	if len(timeout) > 0 {
		limit = timeout[0]
	}
	deadline := time.Now().Add(limit)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("Timed out")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func realpath(path string) (string, error) { return filepath.EvalSymlinks(path) }
