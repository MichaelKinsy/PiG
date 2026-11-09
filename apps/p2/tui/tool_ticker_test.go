package tui_test

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
)

// A running command's card counts the elapsed time with a timer of its own: the timer's goroutine only marks the card, and the card rebuilds its display when
// it renders on the owner loop (-race: the timer used to rebuild it while the loop rendered it).
func TestARunningBashCardsTimerDoesNotRebuildItsDisplayBesideTheRender(t *testing.T) {
	running := components.NewToolExecutionComponent(transcript.ToolView{
		ID: "call", Name: "bash", Args: jsvalue.Obj("command", "sleep 3600"), Status: transcript.ToolRunning, Output: "partial",
	}, pluginRenderers()["bash"], cardContext{darkRef(t)}, true)
	defer running.Dispose()
	// The renderer's timer ticks each second: two ticks land while the card is rendered without pause.
	for deadline := time.Now().Add(2500 * time.Millisecond); time.Now().Before(deadline); {
		running.Render(60)
	}
	mustMatch(t, `(?s)partial.*Elapsed [12]\.`, plainLines(running.Render(60)))
}
