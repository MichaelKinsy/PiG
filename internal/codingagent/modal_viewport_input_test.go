package codingagent

import (
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestEditorSlotSelectorPageKeyMovesSelectionInEveryMode: model-selector.ts is shown in the editor slot, not as an overlay. In fullscreen
// the alternate screen's listener (tui-alt-screen.ts handleViewportInput) would take PageDown for the transcript. Pi PR #7680 (Pi #7629)
// lets a focused selection that captures the selection page keys keep them, so PageDown moves the selection a page in fullscreen as it
// does in regular mode (before the change, regular mode refiltered the search and fullscreen scrolled).
func TestEditorSlotSelectorPageKeyMovesSelectionInEveryMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fullscreen bool
	}{
		{name: "fullscreen", fullscreen: true},
		{name: "regular", fullscreen: false},
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
			items := make([]tui.ModelSelectorItem, 14)
			for i := range items {
				items[i] = tui.ModelSelectorItem{Provider: "fixture", ID: fmt.Sprintf("model-%02d", i)}
			}
			selector := tui.NewStaticModelSelectorComponent("Select model", nil, items, "fixture/model-00")
			selector.SetFilter("model")

			result := make(chan string, 1)
			go func() {
				done := m.showModelSelector(selector)
				spec, _ := m.runModelSelectorInput(m.runCtx, selector)
				done()
				result <- spec
			}()
			input := waitForModalRoute(t, m)
			input <- []byte("\x1b[6~")
			input <- []byte("\r")
			select {
			case got := <-result:
				if got != "fixture/model-10" {
					t.Fatalf("selected %q after PageDown, want fixture/model-10", got)
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
