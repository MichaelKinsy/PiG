package tui

import "testing"

// packages/tui/src/terminal.ts:166-167 `get kittyProtocolActive()` reports the session's Kitty keyboard protocol state, and
// terminal.ts:94 declares it on the Terminal interface.
func TestProcessTerminalKittyProtocolActiveFollowsTheSessionState(t *testing.T) {
	previous := IsKittyProtocolActive()
	t.Cleanup(func() { SetKittyProtocolActive(previous) })
	var terminal Terminal = &ProcessTerminal{}
	for _, want := range []bool{false, true, false} {
		SetKittyProtocolActive(want)
		if got := terminal.KittyProtocolActive(); got != want {
			t.Fatalf("KittyProtocolActive() = %v, want %v", got, want)
		}
	}
}

// packages/tui/src/components/scroll-view.ts:211 `override clear()` throws "ScrollView child cannot be cleared"; the child is
// fixed for the life of the view.
func TestScrollViewClearRejectsClearingTheChild(t *testing.T) {
	view := NewScrollView(&stubComponent{lines: []string{"child"}}, ScrollViewOptions{})
	defer func() {
		if got := recover(); got != "ScrollView child cannot be cleared" {
			t.Fatalf("Clear panicked with %v", got)
		}
	}()
	view.Clear()
	t.Fatal("Clear returned")
}
