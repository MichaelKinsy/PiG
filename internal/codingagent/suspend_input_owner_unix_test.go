//go:build unix

package codingagent

import (
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi ui.stop releases terminal input before returning to the shell. PiG's current stdin owner must be paused and joined, not left reading a cooked/background terminal while the renderer is stopped.
func TestSuspendPausesInputBeforeRestoringTerminal(t *testing.T) {
	joined := make(chan struct{})
	paused := false
	rawRestores, drains := 0, 0
	reader := &interactiveTerminalReader{
		cancel: func() { paused = true; close(joined) },
		done:   joined,
	}
	mode := &InteractiveMode{
		tuiInst:     tui.NewWithOutput(io.Discard, 80, 24),
		inputReader: reader,
		rawRestore: func() {
			rawRestores++
			if !paused {
				t.Error("raw terminal restored before stdin owner was paused")
			}
		},
		rawDrain: func() { drains++ },
	}
	mode.suspendOperations().stop()
	if !paused || reader.cancel != nil {
		t.Error("temporary handoff left stdin owner active")
	}
	if rawRestores != 1 || drains != 0 || mode.rawRestore != nil || mode.rawDrain != nil {
		t.Fatalf("raw restore=%d drain=%d restore retained=%t drain retained=%t", rawRestores, drains, mode.rawRestore != nil, mode.rawDrain != nil)
	}
}
