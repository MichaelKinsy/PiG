package codingagent

import (
	"bytes"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi interactive-mode.ts:3366 refreshes the title/footer on Session name events without rebuilding chat history.
func TestSessionNameEventUpdatesInteractiveFooter(t *testing.T) {
	var output bytes.Buffer
	m := &InteractiveMode{
		tuiInst:    tui.NewWithOutput(&output, 80, 24),
		statusLine: NewFooterComponent(nil, "", nil),
	}
	renders := make(chan func(), 2)
	m.tuiInst.SetRenderDispatcher(func(render func()) { renders <- render })
	t.Cleanup(m.tuiInst.Stop)
	for _, name := range []string{"from extension", ""} {
		output.Reset()
		m.handleAgentEvent(agent.SessionInfoChangedEvent{Name: name})
		if m.statusLine.name != name {
			t.Fatalf("footer name=%q, want %q", m.statusLine.name, name)
		}
		// The title is written on the renderer's terminal at once (updateTerminalTitle); the frame waits for the requested render.
		if want := "\x1b]0;" + tui.BuildTerminalTitle(name, "") + "\x07"; output.String() != want {
			t.Fatalf("name event wrote %q on the terminal, want only the title %q: the transcript is not repainted synchronously", output.String(), want)
		}
		select {
		case render := <-renders:
			render()
		case <-time.After(5 * time.Second):
			t.Fatal("name event did not request a render")
		}
	}
}
