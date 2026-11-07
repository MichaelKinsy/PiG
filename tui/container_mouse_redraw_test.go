package tui

import (
	"strings"
	"testing"
)

type silentLine struct{ line func() string }

func (s *silentLine) Render(int) []string { return []string{s.line()} }
func (*silentLine) Invalidate()           {}

// Pi's Container keeps no row cache and redraws every component after a handled mouse event, so a MouseRegion handler may change what its child
// draws without telling anyone (an extension's click-to-expand card flips a field in a closure). Here a container above a message component (which
// reports itself clean) must draw the card again after the click, though nothing invalidated it.
func TestHandledMouseEventRedrawsComponentsThatChangedSilently(t *testing.T) {
	open := false
	renderer := func(*CustomMessage, MessageRenderOptions) Component {
		return NewMouseRegion(&silentLine{line: func() string {
			if open {
				return "card: open"
			}
			return "card: closed"
		}}, func(event TuiMouseEvent) *TuiMouseEventResult {
			if event.Type != MouseClick {
				return nil
			}
			open = !open
			return &TuiMouseEventResult{Handled: true}
		})
	}
	root := NewContainer(NewContainer(NewCustomMessageComponent(&CustomMessage{CustomType: "card", Content: "x"}, renderer, nil, 1)))
	draw := func() string { return strings.TrimSpace(strings.Join(root.Render(40), "\n")) }

	if got := draw(); got != "card: closed" {
		t.Fatalf("first frame %q", got)
	}
	click := TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft, X: 3, Y: 1, ScreenX: 3, ScreenY: 1, Width: 40, Height: 2}
	if result := DispatchMouseEvent(root, click); result == nil || !result.Handled {
		t.Fatalf("click result %+v, want handled", result)
	}
	if got := draw(); got != "card: open" {
		t.Fatalf("frame after the click %q, want the card redrawn open", got)
	}
	if unhandled := DispatchMouseEvent(root, TuiMouseEvent{Type: MousePress, Button: MouseButtonLeft, X: 3, Y: 1, Width: 40, Height: 2}); unhandled != nil {
		t.Fatalf("press handled: %+v", unhandled)
	}
}
