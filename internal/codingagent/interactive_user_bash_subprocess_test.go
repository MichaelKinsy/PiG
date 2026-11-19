package codingagent

import (
	"os"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func waitUserBashFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.NewTimer(testbudget.Wait(t))
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil && string(data) == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("%s: got %q, err=%v; want %q", path, data, err, want)
		case <-tick.C:
		}
	}
}
