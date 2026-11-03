package tui

import (
	"bytes"
	"strings"
	"testing"
)

// mutableInput is a focusable component whose rendered text is the input it has received.
type mutableInput struct {
	received []string
	focused  bool
}

func (m *mutableInput) Render(int) []string     { return []string{"got:" + strings.Join(m.received, ",")} }
func (m *mutableInput) Invalidate()             {}
func (m *mutableInput) HandleInput(data string) { m.received = append(m.received, data) }
func (m *mutableInput) SetFocused(focused bool) { m.focused = focused }
func renderOnlyComponent(text string) Component {
	return renderFuncComponent(func(int) []string { return []string{text} })
}
func keepCellDimensions(t *testing.T) CellDimensions {
	t.Helper()
	previous := GetCellDimensions()
	t.Cleanup(func() { SetCellDimensions(previous) })
	return previous
}

// tui.ts handleTerminalInput consumes a cell-size reply only after the input listeners: a listener that consumes the reply keeps it from setting the cell size,
// and a listener that replaces other input with a reply makes that reply the one consumed.
func TestHandleTerminalInputCellSizeReplyFollowsTheInputListeners(t *testing.T) {
	previous := keepCellDimensions(t)
	const reply = "\x1b[6;37;19t"
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus := &mutableInput{}
	ui.SetFocus(focus)
	ui.AddInputListener(listener(&[]string{}, "consume", func(data string) *TuiInputResult {
		if data == reply {
			return &TuiInputResult{Consume: true}
		}
		return nil
	}))
	ui.HandleTerminalInput(reply)
	if got := GetCellDimensions(); got != previous {
		t.Fatalf("a reply consumed by a listener set the cell size to %+v", got)
	}
	if len(focus.received) != 0 {
		t.Fatalf("the focused component received %q", focus.received)
	}

	replacing := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	focus = &mutableInput{}
	replacing.SetFocus(focus)
	replacing.AddInputListener(listener(&[]string{}, "replace", func(data string) *TuiInputResult {
		if data == "x" {
			return &TuiInputResult{Data: new(reply)}
		}
		return nil
	}))
	replacing.HandleTerminalInput("x")
	if got := GetCellDimensions(); got != (CellDimensions{WidthPx: 19, HeightPx: 37}) {
		t.Fatalf("a reply produced by a listener left the cell size at %+v", got)
	}
	if len(focus.received) != 0 {
		t.Fatalf("the focused component received %q", focus.received)
	}
}

// tui.ts handleTerminalInput requests the immediate render only when a focused component with handleInput received the input.
func TestHandleTerminalInputRequestsNoRenderWithoutAnInputHandler(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	ui.SetFocus(renderOnlyComponent("plain"))
	ui.HandleTerminalInput("k")
	ui.mu.Lock()
	requested := ui.immediateRenderRequested
	ui.mu.Unlock()
	if requested {
		t.Fatal("input to a focused component without handleInput requested a render")
	}
}

// tui.ts handleTerminalInput passes the input to the focused component and then requests the render, so a render dispatched at once draws the component after it handled the key.
func TestHandleTerminalInputRendersAfterTheFocusedComponentHandledTheKey(t *testing.T) {
	var out bytes.Buffer
	ui := newManualRenderTUI(&out, 40, 5)
	focus := &mutableInput{}
	ui.Add(focus)
	ui.SetFocus(focus)
	ui.SetRenderDispatcher(func(render func()) { render() })
	ui.HandleTerminalInput("k")
	if !strings.Contains(out.String(), "got:k") {
		t.Fatalf("the frame requested by the key was drawn before the component handled it: %q", out.String())
	}
}

// tui.ts handleTerminalInput refreshes overlay visibility and focus restoration before it delivers: when the focused overlay stops being visible the focus flag moves to the component it gives back.
func TestHandleTerminalInputMovesTheFocusFlagWhenTheOverlayHides(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	base, overlay := &mutableInput{}, &mutableInput{}
	ui.SetFocus(base)
	shown := true
	ui.ShowOverlay(overlay, OverlayOptions{visible: func(int, int) bool { return shown }})
	if base.focused || !overlay.focused {
		t.Fatalf("after ShowOverlay base focused=%v overlay focused=%v, want false and true", base.focused, overlay.focused)
	}
	shown = false
	ui.HandleTerminalInput("b")
	if !base.focused || overlay.focused {
		t.Fatalf("after the overlay hid base focused=%v overlay focused=%v, want true and false", base.focused, overlay.focused)
	}
	if len(base.received) != 1 || len(overlay.received) != 0 {
		t.Fatalf("base received %q and overlay %q", base.received, overlay.received)
	}
}

// tui.ts handleTerminalInput restores focus to an overlay that was visible, lost focus because it hid, and is visible again: the next input goes to the overlay and the focus flag moves back to it.
func TestHandleTerminalInputRestoresTheFocusToAnOverlayThatIsVisibleAgain(t *testing.T) {
	ui := newManualRenderTUI(&bytes.Buffer{}, 40, 5)
	base, overlay := &mutableInput{}, &mutableInput{}
	ui.SetFocus(base)
	shown := true
	ui.ShowOverlay(overlay, OverlayOptions{visible: func(int, int) bool { return shown }})
	ui.HandleTerminalInput("a")
	shown = false
	ui.HandleTerminalInput("b")
	shown = true
	ui.HandleTerminalInput("c")
	if !equalStrings(overlay.received, []string{"a", "c"}) || !equalStrings(base.received, []string{"b"}) {
		t.Fatalf("overlay received %v and base %v, want [a c] and [b]", overlay.received, base.received)
	}
	if base.focused || !overlay.focused {
		t.Fatalf("base focused=%v overlay focused=%v, want false and true", base.focused, overlay.focused)
	}
}
