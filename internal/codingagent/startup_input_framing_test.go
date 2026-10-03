package codingagent

import (
	"io"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type framedStartupTerminal struct{ event string }

func (f framedStartupTerminal) StartWithReadError(input func([]byte), _ func(), _ func(error)) error {
	input([]byte(f.event))
	return nil
}
func (framedStartupTerminal) Stop()        {}
func (framedStartupTerminal) Write(string) {}

type startupInputRecorder struct{ events []string }

func (*startupInputRecorder) Invalidate() {}

func (*startupInputRecorder) Render(int) []string    { return []string{"ready"} }
func (r *startupInputRecorder) HandleInput(s string) { r.events = append(r.events, s) }
func (r *startupInputRecorder) Done() bool           { return len(r.events) > 0 }

func TestStartupDoesNotReframeTerminalEvents(t *testing.T) {
	// ProcessTerminal already waited50+150ms before forwarding this rejected prefix.
	synctest.Test(t, func(t *testing.T) {
		takeStartupInput()
		defer takeStartupInput()
		component := &startupInputRecorder{}
		start := time.Now()
		_, err := runStartupComponentWith(component, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, tui.NewWithOutput(io.Discard, 80, 24), framedStartupTerminal{event: "\x1b["})
		if err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("decoded event delayed again by %s", elapsed)
		}
		if !slices.Equal(component.events, []string{"\x1b["}) {
			t.Errorf("events=%q", component.events)
		}
	})
}
