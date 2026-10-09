package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/session-selector.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// thinking-selector.ts:50-53 and session-selector.ts:733-740 `set focused`: the selector propagates the TUI focus to its search
// input (and the session selector also to its rename input); only a focused input emits the cursor marker.
func TestThinkingSelectorPropagatesFocusToItsSearchInput(t *testing.T) {
	selector := NewThinkingSelectorComponent(ai.ThinkingLevel("low"), []ai.ThinkingLevel{ai.ThinkingLevel("low"), ai.ThinkingLevel("high")}, nil, func() {}, nil, ai.ThinkingLevel("low"))
	if !tui.IsFocusable(selector) {
		t.Fatal("ThinkingSelectorComponent is not Focusable")
	}
	for _, want := range []bool{false, true, false} {
		selector.SetFocused(want)
		got := strings.Contains(strings.Join(selector.Render(60), "\n"), widthx.CursorMarker)
		if selector.Focused() != want || got != want {
			t.Fatalf("SetFocused(%v): Focused=%v marker=%v", want, selector.Focused(), got)
		}
	}
}

func TestSessionSelectorPropagatesFocusToBothInputs(t *testing.T) {
	selector := newSessionSelectorFromListers(func(SessionListOptions) ([]SessionInfo, error) { return nil, nil }, func(SessionListOptions) ([]SessionInfo, error) { return nil, nil }, nil, nil, "", nil)
	if !tui.IsFocusable(selector) {
		t.Fatal("SessionSelectorComponent is not Focusable")
	}
	for _, want := range []bool{false, true, false} {
		selector.SetFocused(want)
		if selector.Focused() != want || selector.searchInput.Focused != want || selector.renameInput.Focused != want {
			t.Fatalf("SetFocused(%v): Focused=%v search=%v rename=%v", want, selector.Focused(), selector.searchInput.Focused, selector.renameInput.Focused)
		}
	}
}
