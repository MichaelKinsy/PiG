package codingagent

import (
	"os"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A running bash card ticks its elapsed time until a final result renders (Pi's bash.ts setInterval, cleared by the final render). The card fixture finishes each card it leaves running, so no ticker outlives the test that drew it: left running, each one requests a render every second for the rest of the package's run.
func TestBuiltinToolCardsLeaveNoTickerRunning(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	baseline := testenv.BaselineGoroutines()
	t.Run("running bash card", func(t *testing.T) {
		renderBuiltinToolCards(t, []toolCardProbe{{Tool: "bash", Args: map[string]any{"command": "make test"}, Result: &toolCardResult{}, Partial: true, Width: 80, Theme: "dark", CWD: cwd}})
	})
	baseline.RequireEnded(t, 2*time.Second)
}
