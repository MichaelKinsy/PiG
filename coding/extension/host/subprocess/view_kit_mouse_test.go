package subprocess

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// mouseRecorder is a pi-tui component with handleMouse that keeps every TuiMouseEvent it takes.
type mouseRecorder struct {
	events []tui.TuiMouseEvent
}

func (*mouseRecorder) Render(int) []string { return []string{"probe"} }
func (*mouseRecorder) Invalidate()         {}

func (m *mouseRecorder) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	m.events = append(m.events, event)
	return &tui.TuiMouseDispatchResult{TuiMouseEventResult: tui.TuiMouseEventResult{Handled: true}}
}

// tui-alt-screen.ts handleMouseEvent hands a component the TuiMouseEvent as the terminal parsed it: an extension's mouse event reaches a
// view's component with its shift, alt and ctrl modifiers, wheelDelta and click count intact.
func TestViewSurfaceHandsTheComponentEveryTuiMouseEventField(t *testing.T) {
	probe := &mouseRecorder{}
	s := newViewSurface("k", newViewImageStore())
	s.sendEvent = func(ViewEventPayload) {}
	s.frame = &viewFrame{root: probe, tree: &frontend.ViewNode{}}
	cases := []struct {
		name string
		in   extension.RemoteMouseEvent
		want tui.TuiMouseEvent
	}{
		{"shift", extension.RemoteMouseEvent{Type: "click", Button: "left", X: 1, Y: 0, ScreenX: 11, ScreenY: 5, Width: 9, Height: 1, Shift: true, ClickCount: 2},
			tui.TuiMouseEvent{Type: tui.MouseClick, Button: "left", X: 1, ScreenX: 11, ScreenY: 5, Width: 9, Height: 1, Shift: true, ClickCount: 2}},
		{"alt wheel", extension.RemoteMouseEvent{Type: "wheel", Button: "none", Width: 9, Height: 1, Alt: true, WheelDelta: -3},
			tui.TuiMouseEvent{Type: tui.MouseWheel, Button: "none", Width: 9, Height: 1, Alt: true, WheelDelta: -3}},
		{"ctrl", extension.RemoteMouseEvent{Type: "press", Button: "left", Width: 9, Height: 1, Ctrl: true},
			tui.TuiMouseEvent{Type: tui.MousePress, Button: "left", Width: 9, Height: 1, Ctrl: true}},
	}
	for _, tc := range cases {
		probe.events = nil
		if result := s.HandleViewMouse(tc.in); !result.Handled {
			t.Fatalf("%s: the component did not take the event", tc.name)
		}
		if len(probe.events) != 1 || probe.events[0] != tc.want {
			t.Fatalf("%s: the component took %+v, want %+v", tc.name, probe.events, tc.want)
		}
	}
}
