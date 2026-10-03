package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestEditorSlotSelectorYieldsViewportKeysInFullscreen pins upstream input
// order for a built-in selector. TUI runs its input listeners before the
// focused component, and in fullscreen the alternate screen's listener
// (tui-alt-screen.ts handleViewportInput) consumes tui.altScreen.pageDown
// unless an overlay has focus. model-selector.ts is shown in the editor slot,
// not as an overlay, so in fullscreen PageDown scrolls the viewport and the
// selection stays on the second match. Regular mode has no such listener:
// PageDown reaches handleInput's search-input fallback, whose filterModels
// selects the first match.
func TestEditorSlotSelectorYieldsViewportKeysInFullscreen(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fullscreen bool
		want       string
	}{
		{name: "fullscreen", fullscreen: true, want: "fixture/model-one"},
		{name: "regular", fullscreen: false, want: "fixture/model-two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m *InteractiveMode
			if tc.fullscreen {
				m = newFullscreenProbe(t)
			} else {
				m = newSwitchTuiProbe(t)
				if m.altScreen != nil {
					t.Fatal("regular probe has an alternate screen")
				}
			}
			items := []tui.ModelSelectorItem{{Provider: "fixture", ID: "model-two"}, {Provider: "fixture", ID: "model-one"}}
			selector := tui.NewModelSelector("Select model", nil, items, "fixture/model-two")
			selector.SetFilter("model")
			selector.HandleInput("\x1b[B")

			result := make(chan string, 1)
			go func() {
				spec, _ := m.runEditorSlotModelSelector(m.runCtx, selector, nil)
				result <- spec
			}()
			input := waitForModalRoute(t, m)
			input <- []byte("\x1b[6~")
			input <- []byte("\r")
			select {
			case got := <-result:
				if got != tc.want {
					t.Fatalf("selected %q after PageDown, want %q", got, tc.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("model selector did not return")
			}
		})
	}
}

func waitForModalRoute(t *testing.T, m *InteractiveMode) chan []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if input, _ := m.modalRoute(); input != nil {
			return input
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("modal input route was not armed")
	return nil
}
