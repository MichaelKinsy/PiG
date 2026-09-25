package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestEditorSlotSelectorsDropKeyReleases asserts the outcome of a complete key
// event for the selectors reached from the editor slot, /model first among them.
//
// These loops receive sequences decoded by the process StdinBuffer before
// focus routing. The focused-component filter must still drop Kitty releases:
func TestEditorSlotSelectorsDropKeyReleases(t *testing.T) {
	// A Kitty press/release pair for Down, as Ghostty emits it.
	const (
		downPress   = "\x1b[B"
		downRelease = "\x1b[1;1:3B"
	)

	items := []tui.ModelSelectorItem{
		{Provider: "p", ID: "one"},
		{Provider: "p", ID: "two"},
		{Provider: "p", ID: "three"},
		{Provider: "p", ID: "four"},
	}
	newSelector := func() *tui.ModelSelector {
		return tui.NewModelSelector("Select model", items, items, "p/one")
	}
	// The highlighted row is what the user sees move, so compare frames.
	render := func(s *tui.ModelSelector) string { return strings.Join(s.Render(80), "\n") }

	// Drive production-equivalent decoding followed by focused delivery.
	feed := func(s *tui.ModelSelector, b *StdinBuffer, data string) {
		for _, chunk := range dropKeyReleases(s, b.ProcessString(data)) {
			s.HandleInput(chunk)
		}
	}

	pressOnly := newSelector()
	var pressOnlyBuffer StdinBuffer
	feed(pressOnly, &pressOnlyBuffer, downPress)
	want := render(pressOnly)

	t.Run("release in the same read", func(t *testing.T) {
		sel := newSelector()
		var b StdinBuffer
		feed(sel, &b, downPress+downRelease)
		if got := render(sel); got != want {
			t.Errorf("one Down keystroke did not land where a press alone lands; the release moved the cursor again")
		}
	})

	t.Run("release in a later read", func(t *testing.T) {
		sel := newSelector()
		var b StdinBuffer
		feed(sel, &b, downPress)
		feed(sel, &b, downRelease)
		if got := render(sel); got != want {
			t.Errorf("one Down keystroke did not land where a press alone lands; the release moved the cursor again")
		}
	})
}
