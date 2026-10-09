package tui

import (
	"bytes"
	"testing"
)

// TestAltScreenResetTextSelectionCases mirrors tui-alt-screen.ts resetTextSelection (#9311): a host replacing the transcript drops the selection and the multi-click history, so the next click is a first click.
func TestAltScreenResetTextSelectionCases(t *testing.T) {
	click := func(renderer *TuiAltScreen) {
		renderer.HandleViewportInput("\x1b[<0;2;1M")
		renderer.HandleViewportInput("\x1b[<0;2;1m")
	}
	for _, test := range []struct {
		name          string
		reset         bool
		wantSelection bool
	}{
		{name: "double click without reset selects the word", wantSelection: true},
		{name: "reset makes the next click a first click", reset: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			renderer := newAltScreenForTest(&out, 40, 6, TuiAltScreenOptions{})
			renderer.previousScreen = []string{"hello world"}
			click(renderer)
			if test.reset {
				renderer.ResetTextSelection()
			}
			click(renderer)
			if got := renderer.HasActiveSelection(); got != test.wantSelection {
				t.Fatalf("HasActiveSelection() = %t, want %t", got, test.wantSelection)
			}
		})
	}
	t.Run("drag selection is dropped", func(t *testing.T) {
		var out bytes.Buffer
		renderer := newAltScreenForTest(&out, 40, 6, TuiAltScreenOptions{})
		renderer.previousScreen = []string{"hello world"}
		renderer.HandleViewportInput("\x1b[<0;1;1M")
		renderer.HandleViewportInput("\x1b[<32;5;1M")
		renderer.HandleViewportInput("\x1b[<0;5;1m")
		if !renderer.HasActiveSelection() {
			t.Fatal("drag should select")
		}
		renderer.ResetTextSelection()
		if renderer.HasActiveSelection() {
			t.Fatal("ResetTextSelection kept the selection")
		}
	})
}
