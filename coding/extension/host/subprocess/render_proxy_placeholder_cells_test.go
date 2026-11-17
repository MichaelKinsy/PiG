package subprocess

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// D647, the cells its record marks "read, not run". A renderer card is drawn as its one-row placeholder from the first frame until the frame that follows the extension's
// first answer. Pi draws the component the renderer returned in the first frame (custom-message.ts rebuild). What the user can do to the placeholder in that time is pinned here.

// mouseEvent is a mouse event of type at cell (x, y) of a card row that is 80 cells wide.
func mouseEvent(kind tui.TuiMouseEventType, x, y int) tui.TuiMouseEvent {
	event := clickAt(x, y)
	event.Type, event.ClickCount = kind, 0
	if kind == tui.MouseWheel {
		event.Button, event.WheelDelta = tui.MouseButtonNone, 1
	}
	return event
}

// Press, release, move, drag and wheel on the placeholder are left to the host's selection and scrolling: none is taken, none is queued for the extension. Pi's component, which is
// there in the first frame, takes the events its regions name. Once the extension has answered and its component is drawn, a region that names them takes them: the difference is
// the window of the placeholder.
func TestRenderProxyPlaceholderLeavesPressDragAndWheelToTheHost(t *testing.T) {
	events := []tui.TuiMouseEventType{tui.MousePress, tui.MouseDrag, tui.MouseRelease, tui.MouseMove, tui.MouseWheel}
	rig := newMouseRig(t, extension.NewRenderIdentity())
	env := rig.held(t)
	for _, kind := range events {
		if result := rig.component.HandleMouse(mouseEvent(kind, 2, 0)); result != nil {
			t.Errorf("%s on the placeholder answered %+v, want unhandled: the host's selection and scrolling take it", kind, result)
		}
		rig.component.mu.Lock()
		queued := len(rig.component.events)
		rig.component.mu.Unlock()
		if queued != 0 {
			t.Errorf("a %s on the placeholder queued %d mouse events for the extension, want none", kind, queued)
		}
	}
	view := `{"root":{"kind":"mouse-region","id":"card","region":{"types":["press","drag","release","move","wheel"]},"children":[{"kind":"text","text":"hello","paddingX":0,"paddingY":0}]}}`
	rig.reply(t, env, RenderResult{View: json.RawMessage(view)})
	waitForRenderProxy(t, func() bool { return rig.invalidated.Load() > 0 })
	rig.component.Render(80)
	for _, kind := range events {
		if result := rig.component.HandleMouse(mouseEvent(kind, 2, 0)); result == nil || !result.Handled {
			t.Errorf("%s on the drawn card answered %+v, want handled by the region that names it", kind, result)
		}
	}
}

// The host's selection reads the rows that are drawn: a selection made over the placeholder row copies "[card]", where Pi's would copy the component's text; once the component is
// drawn the same gesture copies its text.
func TestRenderProxyPlaceholderIsWhatSelectionAndCopyRead(t *testing.T) {
	rig := newMouseRig(t, extension.NewRenderIdentity())
	var out bytes.Buffer
	ui := tui.NewTuiAltScreenWithOutput(&out, 40, 6, tui.TuiAltScreenOptions{})
	ui.Add(rig.component)
	ui.Start()
	t.Cleanup(ui.Stop)
	first := readRenderRequest(t, rig)
	copied := func() string {
		t.Helper()
		out.Reset()
		ui.HandleViewportInput("\x1b[<0;1;1M")
		ui.HandleViewportInput("\x1b[<32;40;1M")
		ui.HandleViewportInput("\x1b[<0;40;1m")
		_, payload, ok := strings.Cut(out.String(), "\x1b]52;c;")
		if !ok {
			t.Fatalf("the selection wrote no clipboard text; out=%q", out.String())
		}
		payload, _, _ = strings.Cut(payload, "\x07")
		payload, _, _ = strings.Cut(payload, "\x1b\\")
		text, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			t.Fatalf("clipboard payload %q: %v", payload, err)
		}
		return strings.TrimSpace(string(text))
	}
	if got := copied(); got != "[card]" {
		t.Errorf("a selection over the placeholder copied %q, want the placeholder's text [card]", got)
	}
	rig.reply(t, first, RenderResult{Lines: []string{"the card"}})
	waitForRenderProxy(t, func() bool { return rig.invalidated.Load() > 0 })
	ui.Render()
	if got := copied(); got != "the card" {
		t.Errorf("a selection over the drawn card copied %q, want the component's text", got)
	}
}

// readRenderRequest reads the proxy's first request, which the first frame started.
func readRenderRequest(t *testing.T, r *mouseRig) Envelope {
	t.Helper()
	env, first := r.request(t)
	if first.Key == "" || !first.Rerender {
		t.Fatalf("first request key=%q rerender=%t", first.Key, first.Rerender)
	}
	return env
}

// The placeholder is one row whatever the component's height, so the rows below a card move when the component replaces it: Pi draws them once, below the component.
func TestRenderProxyRowsBelowATallerComponentMoveWhenItReplacesItsPlaceholder(t *testing.T) {
	rig := newMouseRig(t, extension.NewRenderIdentity())
	list := tui.NewContainer()
	list.Add(rig.component)
	list.Add(tui.NewText("below"))
	rows := func() []string {
		var trimmed []string
		for _, row := range list.Render(80) {
			trimmed = append(trimmed, strings.TrimSpace(row))
		}
		return trimmed
	}
	if got, want := rows(), []string{"[card]", "below"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows with the placeholder = %q, want %q", got, want)
	}
	env := readRenderRequest(t, rig)
	rig.reply(t, env, RenderResult{Lines: []string{"one", "two", "three"}})
	waitForRenderProxy(t, func() bool { return rig.invalidated.Load() > 0 })
	if got, want := rows(), []string{"one", "two", "three", "below"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows with the component = %q, want %q: the row below moved", got, want)
	}
}
