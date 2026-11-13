package codingagent

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D614): a mount of slot dialog on the production renderer: the frame is the renderer's overlay, the one
// Pi's ctx.ui.custom draws an extension's component in.

const viewDialogProbeView = `{"root":{"kind":"vstack","children":[{"kind":"text","text":"dialog body row"},` +
	`{"kind":"select-list","id":"pick","maxVisible":4,"items":[{"value":"a","label":"Alpha"},{"value":"b","label":"Beta"}]}]},"focus":"pick"}`

const viewDialogProbePanel = `"slot":"panel","view":{"root":{"kind":"select-list","id":"pick","maxVisible":4,` +
	`"items":[{"value":"a","label":"Panel one"},{"value":"b","label":"Panel two"}]},"focus":"pick"}`

func viewDialogProbeMount(key string) string {
	return `{"key":"` + key + `","slot":"dialog","title":"Probe ` + key + `","view":` + viewDialogProbeView + `}`
}

// mounted returns the client's mount of the extension ext's key.
func (p *viewModeProbe) mounted(key string) *viewMount {
	p.t.Helper()
	m := p.m.viewClient.Load().mounts[viewMountKey{"ext", key}]
	if m == nil {
		p.t.Fatalf("the client holds no mount %s", key)
	}
	return m
}

// framed reports whether the renderer holds a visible overlay: a frame it drew at the last frame.
func (p *viewModeProbe) framed() bool {
	for _, m := range p.m.viewClient.Load().floats {
		if _, drawn := m.frame.(*viewFrame).handle.GetBounds(); drawn {
			return true
		}
	}
	return false
}

func (p *viewModeProbe) key(data string) {
	p.t.Helper()
	if err := p.m.handleKey(p.t.Context(), data); err != nil {
		p.t.Fatal(err)
	}
}

func viewDialogModes(t *testing.T, run func(t *testing.T, p *viewModeProbe)) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) { run(t, newViewModeProbe(t, mode, true)) })
		})
	}
}

// The dialog is drawn in the renderer's titled frame with the focus, its keys reach its node and none reaches the
// editor, and the panel under it keeps its component, its selection and, when the dialog closes, the focus it had. The
// frame's size is the one the abilities claim for the slot.
func TestTerminalViewClientDrawsADialogInTheRenderersFrame(t *testing.T) {
	viewDialogModes(t, func(t *testing.T, p *viewModeProbe) {
		p.must("ui.view.mount", `{"key":"list","focus":[{"node":"pick","request":"mount"}],`+viewDialogProbePanel+`}`)
		p.frame()
		panel := p.mounted("list")
		if p.m.tuiInst.GetFocusedComponent() != panel {
			t.Fatal("the panel that asked for the focus does not hold it")
		}
		p.key("\x1b[B")
		p.frame()
		under := strings.Join(panel.Render(40), "\n")

		ref := p.must("ui.view.mount", viewDialogProbeMount("map"))
		frame := widthx.StripAnsi(p.frame())
		for _, want := range []string{"Probe map", "dialog body row", "Alpha", "Beta"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("the frame with the dialog lacks %q:\n%s", want, frame)
			}
		}
		dialog := p.mounted("map")
		if !dialog.focused || panel.focused || !p.framed() {
			t.Fatalf("the dialog focused %v, the panel focused %v, overlay %v", dialog.focused, panel.focused, p.framed())
		}

		bounds, ok := dialog.frame.(*viewFrame).handle.GetBounds()
		if !ok {
			t.Fatal("the renderer drew no frame for the dialog")
		}
		columns, rows := p.m.tuiInst.Width(), p.m.tuiInst.Height()
		options := viewDialogOverlay("")
		if want := int(float64(columns) * options.WidthFraction); bounds.Width != want {
			t.Fatalf("the frame is %d columns wide in a terminal of %d, want %d", bounds.Width, columns, want)
		}
		if want := int(float64(rows) * options.HeightFraction); bounds.Height != want {
			t.Fatalf("the frame is %d rows high in a terminal of %d, want %d", bounds.Height, rows, want)
		}
		claims := map[string]string{}
		for _, slot := range p.bridge.Views().Abilities().SlotClaims {
			if slot.Slot == uiview.SlotDialog {
				for _, c := range slot.Claims {
					claims[c.Name] = string(c.Value)
				}
			}
		}
		if want := map[string]string{"size.columns": strconv.Itoa(bounds.Width - viewDialogBorder), "size.rows": strconv.Itoa(bounds.Height - viewDialogBorder)}; !mapsEqual(claims, want) {
			t.Fatalf("the dialog slot claims %v and the frame holds %v", claims, want)
		}
		if dialog.lineWidth != bounds.Width-viewDialogBorder {
			t.Fatalf("the dialog was laid out at %d columns in a frame of %d", dialog.lineWidth, bounds.Width)
		}

		before := strings.Join(dialog.Render(dialog.lineWidth), "\n")
		p.key("\x1b[B")
		p.key("x")
		p.frame()
		if after := strings.Join(dialog.Render(dialog.lineWidth), "\n"); after == before {
			t.Fatal("Down did not move the selection of the dialog's list")
		}
		if text := p.m.editor.Text(); text != "" {
			t.Fatalf("a key of the focused dialog reached the editor: %q", text)
		}
		if p.mounted("list") != panel || strings.Join(panel.Render(40), "\n") != under {
			t.Fatal("the panel under the dialog changed")
		}

		p.must("ui.view.hide", `{"mount":`+string(ref)+`}`)
		p.frame()
		if p.framed() || dialog.focused || p.m.tuiInst.GetFocusedComponent() != panel || !panel.focused {
			t.Fatalf("after hide: overlay %v, the panel focused %v", p.framed(), panel.focused)
		}
		if strings.Join(panel.Render(40), "\n") != under {
			t.Fatal("the panel lost its selection under the dialog")
		}
		p.must("ui.view.show", `{"mount":`+string(ref)+`}`)
		p.frame()
		if !p.framed() || !dialog.focused {
			t.Fatal("show did not open the frame again with the focus")
		}
		p.must("ui.view.remove", `{"mount":`+string(ref)+`}`)
		p.frame()
		if p.m.tuiInst.HasOverlay() || p.m.tuiInst.GetFocusedComponent() != panel {
			t.Fatal("remove left the frame or did not return the focus")
		}
	})
}

// The key that interrupts leaves the dialog for the editor and the frame stays; the next press interrupts. F6 moves
// between the editor and the dialog and passes a focusable panel by. A key typed while the dialog is drawn without the
// focus is the editor's.
func TestTerminalViewClientEscapeLeavesTheDialogDrawn(t *testing.T) {
	viewDialogModes(t, func(t *testing.T, p *viewModeProbe) {
		interrupts := 0
		p.m.defaultCustomEditor().OnEscape = func() { interrupts++ }
		p.must("ui.view.mount", `{"key":"list",`+viewDialogProbePanel+`}`)
		p.must("ui.view.mount", viewDialogProbeMount("map"))
		p.frame()
		panel, dialog := p.mounted("list"), p.mounted("map")
		if !dialog.focused {
			t.Fatal("the dialog shown over an empty editor does not hold the focus")
		}
		p.key("\x1b")
		if interrupts != 0 || dialog.focused || p.m.tuiInst.GetFocusedComponent() != p.m.editor || !p.framed() {
			t.Fatalf("after Escape: interrupts %d, the dialog focused %v, overlay %v", interrupts, dialog.focused, p.framed())
		}
		p.key("\x1b")
		if interrupts != 1 {
			t.Fatalf("the second Escape interrupted %d times, want 1", interrupts)
		}
		for i, want := range []bool{true, false, true, false} {
			if _, consumed := p.m.tuiInst.RunInputListeners("\x1b[17~"); !consumed || dialog.focused != want || panel.focused {
				t.Fatalf("F6 press %d: consumed %v, the dialog focused %v (want %v), the panel focused %v", i+1, consumed, dialog.focused, want, panel.focused)
			}
		}
		if p.m.tuiInst.GetFocusedComponent() != p.m.editor {
			t.Fatal("F6 from the dialog did not return the focus to the editor")
		}
		p.key("z")
		p.frame()
		if text := p.m.editor.Text(); text != "z" || !p.framed() {
			t.Fatalf("a key typed beside the dialog: the editor holds %q, overlay %v", text, p.framed())
		}
	})
}

// Several live dialogs are stacked: the last shown has the keyboard, and when it ends the one under it has it again.
func TestTerminalViewClientStacksDialogs(t *testing.T) {
	viewDialogModes(t, func(t *testing.T, p *viewModeProbe) {
		p.must("ui.view.mount", viewDialogProbeMount("one"))
		p.frame()
		second := p.must("ui.view.mount", viewDialogProbeMount("two"))
		frame := widthx.StripAnsi(p.frame())
		one, two := p.mounted("one"), p.mounted("two")
		if !two.focused || one.focused || !strings.Contains(frame, "Probe two") {
			t.Fatalf("two live dialogs: the second focused %v, the first %v", two.focused, one.focused)
		}
		p.must("ui.view.hide", `{"mount":`+string(second)+`}`)
		p.frame()
		if !one.focused || two.focused || !p.framed() {
			t.Fatalf("after the top dialog ended: the first focused %v", one.focused)
		}
		p.key("\x1b[B")
		p.frame()
		if text := p.m.editor.Text(); text != "" {
			t.Fatalf("a key of the dialog under the one that ended reached the editor: %q", text)
		}
	})
}

// A dialog shown while the editor holds text is drawn without the focus, so what the user types stays the editor's.
func TestTerminalViewClientDialogLeavesATypingUserTheKeys(t *testing.T) {
	viewDialogModes(t, func(t *testing.T, p *viewModeProbe) {
		p.typeText("hello")
		p.must("ui.view.mount", viewDialogProbeMount("map"))
		p.frame()
		dialog := p.mounted("map")
		if dialog.focused || !p.framed() || p.m.tuiInst.GetFocusedComponent() != p.m.editor {
			t.Fatalf("a dialog shown over a typing user: focused %v, overlay %v", dialog.focused, p.framed())
		}
		p.key("!")
		if text := p.m.editor.Text(); text != "hello!" {
			t.Fatalf("the editor holds %q", text)
		}
		if _, consumed := p.m.tuiInst.RunInputListeners("\x1b[17~"); !consumed || !dialog.focused {
			t.Fatal("F6 did not give the focus to the dialog")
		}
	})
}

// In fullscreen mode a click inside the frame gives the dialog the focus.
func TestTerminalViewClientClickFocusesTheDialog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newViewModeProbe(t, "fullscreen", true)
		p.must("ui.view.mount", viewDialogProbeMount("map"))
		p.frame()
		dialog := p.mounted("map")
		p.key("\x1b")
		p.frame()
		bounds, ok := dialog.frame.(*viewFrame).handle.GetBounds()
		if dialog.focused || !ok {
			t.Fatalf("before the click: the dialog focused %v, frame %v", dialog.focused, ok)
		}
		// The list's first item, three cells into the frame. The frame's border is one cell, and the report is 1-based.
		item := slices.IndexFunc(dialog.Render(dialog.lineWidth), func(line string) bool { return strings.Contains(line, "Alpha") })
		if item < 0 {
			t.Fatal("the dialog draws no list item")
		}
		column, row := bounds.Col+4, bounds.Row+1+item+1
		p.m.altScreen.HandleViewportInput(fmt.Sprintf("\x1b[<0;%d;%dM", column, row))
		p.m.altScreen.HandleViewportInput(fmt.Sprintf("\x1b[<0;%d;%dm", column, row))
		p.frame()
		if !dialog.focused {
			t.Fatal("a click inside the frame did not give the dialog the focus")
		}
	})
}

// A frame with a dialog open lays the dialog's view out once, not for every frame.
func TestTerminalViewClientDialogIsLaidOutOnceForItsFrames(t *testing.T) {
	viewDialogModes(t, func(t *testing.T, p *viewModeProbe) {
		p.must("ui.view.mount", viewDialogProbeMount("map"))
		p.frame()
		dialog := p.mounted("map")
		revision, first := dialog.surface.Revision(), &dialog.lines[0]
		for range 20 {
			p.m.tuiInst.Render()
		}
		p.term.take()
		if dialog.surface.Revision() != revision || &dialog.lines[0] != first {
			t.Fatal("a frame laid the dialog's view out again")
		}
	})
}

var _ tui.Component = (*viewMount)(nil)
