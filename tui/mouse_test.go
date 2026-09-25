package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func leftMouse(eventType TuiMouseEventType, x, y, width, height int) TuiMouseEvent {
	event := TuiMouseEvent{Type: eventType, Button: MouseButtonLeft, X: x, Y: y, ScreenX: x, ScreenY: y, Width: width, Height: height}
	if eventType == MouseClick {
		event.ClickCount = 1
	}
	return event
}

// mouseProbe is a leaf component that records events and answers with result.
type mouseProbe struct {
	invalidatable
	lines  []string
	events []TuiMouseEvent
	result *TuiMouseEventResult
}

func (p *mouseProbe) Render(int) []string { return p.lines }

func (p *mouseProbe) HandleMouse(event TuiMouseEvent) *TuiMouseDispatchResult {
	p.events = append(p.events, event)
	if p.result == nil {
		return nil
	}
	return &TuiMouseDispatchResult{TuiMouseEventResult: *p.result}
}

func TestDispatchMouseEventBindsHandledResultToTarget(t *testing.T) {
	probe := &mouseProbe{lines: []string{"x"}, result: &TuiMouseEventResult{Focus: true}}
	event := TuiMouseEvent{Type: MousePress, X: 2, Y: 1, ScreenX: 12, ScreenY: 5, Width: 8, Height: 3}
	result := DispatchMouseEvent(probe, event)
	if result == nil || !result.Handled || result.FocusTarget != probe {
		t.Fatalf("focus result = %+v, want handled with focus target", result)
	}
	want := TuiMouseDispatchTarget{Component: probe, OriginX: 10, OriginY: 4, Width: 8, Height: 3}
	if result.Target != want {
		t.Fatalf("target = %+v, want %+v", result.Target, want)
	}
	if again := DispatchMouseEvent(NewMouseRegion(probe, func(TuiMouseEvent) *TuiMouseEventResult { return nil }), event); again == nil || again.Target != want {
		t.Fatalf("an already-dispatched result must pass through unchanged: %+v", again)
	}

	probe.result = &TuiMouseEventResult{}
	if DispatchMouseEvent(probe, event) != nil {
		t.Fatal("a result that neither handles, captures, nor focuses is unhandled")
	}
	if DispatchMouseEvent(NewText("plain"), event) != nil {
		t.Fatal("a component without a mouse handler is unhandled")
	}
}

func TestRetargetMouseEventRecreatesLocalCoordinates(t *testing.T) {
	event := TuiMouseEvent{Type: MouseDrag, X: 0, Y: 0, ScreenX: 15, ScreenY: 7, Width: 80, Height: 24}
	got := RetargetMouseEvent(event, TuiMouseDispatchTarget{OriginX: 10, OriginY: 4, Width: 8, Height: 3})
	if got.X != 5 || got.Y != 3 || got.Width != 8 || got.Height != 3 || got.ScreenX != 15 || got.ScreenY != 7 {
		t.Fatalf("retargeted event = %+v", got)
	}
}

func TestContainerHandleMouseRoutesByRenderedRows(t *testing.T) {
	first := &mouseProbe{lines: []string{"a", "b"}, result: &TuiMouseEventResult{Handled: true}}
	second := &mouseProbe{lines: []string{"c"}, result: &TuiMouseEventResult{Handled: true}}
	container := NewContainer(first, second)
	container.Render(10)

	result := container.HandleMouse(leftMouse(MousePress, 3, 2, 10, 3))
	if result == nil || result.Target.Component != second || len(second.events) != 1 {
		t.Fatalf("row 2 must reach the second child: %+v", result)
	}
	if got := second.events[0]; got.Y != 0 || got.Height != 1 || got.X != 3 || got.ScreenY != 2 {
		t.Fatalf("child event = %+v, want y 0 of a one-row child", got)
	}
	if result.Target.OriginY != 2 {
		t.Fatalf("target origin y = %d, want 2", result.Target.OriginY)
	}
	if container.HandleMouse(leftMouse(MousePress, 0, 3, 10, 3)) != nil {
		t.Fatal("rows outside the container bounds are unhandled")
	}
	// A width change re-measures instead of using the cached layout.
	if result := container.HandleMouse(leftMouse(MousePress, 0, 1, 20, 3)); result == nil || result.Target.Component != first {
		t.Fatalf("row 1 at a new width must reach the first child: %+v", result)
	}
}

func TestBoxHandleMouseSkipsPadding(t *testing.T) {
	probe := &mouseProbe{lines: []string{"inner"}, result: &TuiMouseEventResult{Handled: true}}
	box := NewBox()
	box.AddChild(probe)
	box.Render(10)
	if box.HandleMouse(leftMouse(MousePress, 3, 0, 10, 3)) != nil {
		t.Fatal("the top padding row is not content")
	}
	if box.HandleMouse(leftMouse(MousePress, 0, 1, 10, 3)) != nil {
		t.Fatal("the left padding column is not content")
	}
	result := box.HandleMouse(leftMouse(MousePress, 3, 1, 10, 3))
	if result == nil || probe.events[0].X != 2 || probe.events[0].Y != 0 || probe.events[0].Width != 8 {
		t.Fatalf("content press must reach the child in content coordinates: %+v %+v", result, probe.events)
	}
}

func TestMouseRegionPrefersNestedHandlers(t *testing.T) {
	inner := &mouseProbe{lines: []string{"inner"}}
	var regionEvents []TuiMouseEventType
	region := NewMouseRegion(inner, func(event TuiMouseEvent) *TuiMouseEventResult {
		regionEvents = append(regionEvents, event.Type)
		return &TuiMouseEventResult{Handled: true}
	})
	if got := region.Render(10); len(got) != 1 || got[0] != "inner" {
		t.Fatalf("region render = %q, want the child unchanged", got)
	}
	result := DispatchMouseEvent(region, leftMouse(MouseClick, 1, 0, 10, 1))
	if result == nil || result.Target.Component != region || len(regionEvents) != 1 {
		t.Fatalf("an unhandled child falls back to the region handler: %+v", result)
	}
	inner.result = &TuiMouseEventResult{Handled: true}
	result = DispatchMouseEvent(region, leftMouse(MouseClick, 1, 0, 10, 1))
	if result == nil || result.Target.Component != inner || len(regionEvents) != 1 {
		t.Fatalf("a handling child wins over the region: %+v", result)
	}
}

// TestInputPositionsCursorOnPress ports upstream mouse-components.test.ts
// "positions a single-line input cursor on press".
func TestInputPositionsCursorOnPress(t *testing.T) {
	input := NewInput(InputOptions{})
	input.SetText("hello")
	input.Render(20)
	if result := input.HandleMouse(leftMouse(MousePress, 4, 0, 20, 1)); result == nil || !result.Handled {
		t.Fatalf("press result = %+v, want handled", result)
	}
	input.HandleInput("X")
	if got := input.Text(); got != "heXllo" {
		t.Fatalf("value = %q, want heXllo", got)
	}
}

// TestInputCustomPromptAndStyledPlaceholder ports upstream input.test.ts
// "supports a custom prompt and styled placeholder".
func TestInputCustomPromptAndStyledPlaceholder(t *testing.T) {
	empty := ""
	input := NewInput(InputOptions{
		Prompt:           &empty,
		Placeholder:      "Find transcript",
		PlaceholderStyle: func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
	})
	input.Focused = true
	line := input.Render(20)[0]
	if !strings.Contains(line, "\x1b[2m") {
		t.Fatalf("placeholder is not styled: %q", line)
	}
	if got := strings.TrimRight(widthx.StripTerminalSequences(line), " "); got != "Find transcript" {
		t.Fatalf("placeholder render = %q", got)
	}
	input.HandleInput("n")
	if got := strings.TrimRight(widthx.StripTerminalSequences(input.Render(20)[0]), " "); got != "n" {
		t.Fatalf("populated render = %q", got)
	}
}

// TestInputCallbacksDoNotLatch pins upstream Input's submit/escape contract:
// NewInput calls OnSubmit/OnEscape and keeps accepting input.
func TestInputCallbacksDoNotLatch(t *testing.T) {
	input := NewInput(InputOptions{})
	var submitted []string
	escapes := 0
	input.OnSubmit = func(value string) { submitted = append(submitted, value) }
	input.OnEscape = func() { escapes++ }
	input.HandleInput("a")
	input.HandleInput("\r")
	input.HandleInput("\x1b")
	input.HandleInput("b")
	if len(submitted) != 1 || submitted[0] != "a" || escapes != 1 || input.Done() || input.Text() != "ab" {
		t.Fatalf("submitted=%q escapes=%d done=%v value=%q", submitted, escapes, input.Done(), input.Text())
	}
}

func TestOverlayBoundsAndMouseHitTesting(t *testing.T) {
	tu := NewWithOutput(io.Discard, 20, 6)
	probe := &mouseProbe{lines: []string{"one", "two"}, result: &TuiMouseEventResult{Handled: true, Focus: true}}
	handle := tu.OpenOverlay(probe, OverlayOptions{width: overlayCells(6), anchor: overlayTopLeft, row: overlayCells(2), col: overlayCells(3)})
	if _, ok := handle.GetBounds(); ok {
		t.Fatal("an overlay has no bounds before it renders")
	}
	tu.composeOverlayLines(make([]string, 6), 20, 6)
	bounds, ok := handle.GetBounds()
	if want := (OverlayBounds{Row: 2, Col: 3, Width: 6, Height: 2}); !ok || bounds != want {
		t.Fatalf("bounds = %+v %v, want %+v", bounds, ok, want)
	}
	if !tu.isOverlayFocused() {
		t.Fatal("a capturing overlay is focused")
	}

	hit := tu.dispatchMouseToOverlay(TuiMouseEvent{Type: MousePress, ScreenX: 4, ScreenY: 3, Width: 20, Height: 6})
	if !hit.hit || hit.result == nil || hit.result.FocusTarget != probe || probe.events[0].X != 1 || probe.events[0].Y != 1 {
		t.Fatalf("overlay hit = %+v events=%+v", hit, probe.events)
	}
	if miss := tu.dispatchMouseToOverlay(TuiMouseEvent{Type: MousePress, ScreenX: 0, ScreenY: 0}); miss.hit {
		t.Fatal("a press outside every overlay is not a hit")
	}
	if got := tu.resolveMouseFocusTarget(probe); got != probe {
		t.Fatalf("focus target = %v, want the overlay", got)
	}

	handle.setHidden(true)
	if _, ok := handle.GetBounds(); ok {
		t.Fatal("a hidden overlay reports no bounds")
	}
	if tu.isOverlayFocused() {
		t.Fatal("a hidden overlay is not focused")
	}
}
