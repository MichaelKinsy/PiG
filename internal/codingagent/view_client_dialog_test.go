package codingagent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D614): the terminal draws a mount of slot dialog in a frame over the screen. These tests drive the
// client against a loop that stands in for the interactive one; view_client_dialog_mode_test.go drives the renderer.

func viewDialogContent() uiview.Content {
	return uiview.Content{View: viewList("where"), Intents: []uiview.IntentDecl{viewPickIntent}}
}

// viewPicks records the arguments the plugin behind f is sent.
func viewPicks(f *viewFixture) *[]string {
	var picks []string
	f.host.invoke = func(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
		picks = append(picks, inv.Mount.Key+" "+string(inv.Arg))
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	return &picks
}

// The cells inside a dialog's frame are the fraction of the terminal the frame takes less its border. The pair 120 by
// 40 is the example the D614 record gives.
func TestViewDialogCells(t *testing.T) {
	for _, terminal := range []int{1, 2, 3, 10, 24, 40, 80, 120, 500} {
		want := max(1, int(float64(terminal)*viewDialogOverlay("").WidthFraction)-viewDialogBorder)
		if got := viewDialogCells(terminal); got != want {
			t.Errorf("viewDialogCells(%d) = %d, want %d", terminal, got, want)
		}
	}
	if o := viewDialogOverlay(""); o.WidthFraction != o.HeightFraction {
		t.Fatalf("the frame takes %v of the columns and %v of the rows, and one function sizes both", o.WidthFraction, o.HeightFraction)
	}
	if columns, rows := viewDialogSlotColumns(120), viewDialogSlotRows(40); columns != 94 || rows != 30 {
		t.Fatalf("a terminal of 120 by 40 gives a dialog %d by %d, and the record says 94 by 30", columns, rows)
	}
}

// A live dialog mount is drawn in one frame titled with its key, outside the widget containers, and holds the focus
// with or without a focus record. Its keys reach its node. What is under it keeps its component and its state.
func TestViewClientDrawsADialogInAFrameWithTheFocus(t *testing.T) {
	f := newViewFixture(t)
	picks := viewPicks(f)
	f.mount("p", uiview.SlotPanel, uiview.Content{View: viewList("choose"), Intents: []uiview.IntentDecl{viewPickIntent}, Focus: []uiview.Focus{{Node: "pick", Request: "mount"}}})
	panel := f.loop.drawn()[0]
	panel.HandleInput("\x1b[B")
	under := viewRows(panel)
	layouts := len(f.loop.laid)

	ref := f.mount("d", uiview.SlotDialog, viewDialogContent())
	open := f.loop.open()
	if len(open) != 1 || open[0].title != "d" {
		t.Fatalf("frames after the mount: %d, want one titled d", len(open))
	}
	dialog := open[0].mount
	if f.loop.focused != dialog || !dialog.focused || panel.focused {
		t.Fatalf("the dialog does not hold the focus: focused %v, dialog %v, panel %v", f.loop.focused == dialog, dialog.focused, panel.focused)
	}
	if len(f.loop.laid) != layouts || slices.Contains(f.loop.drawn(), dialog) {
		t.Fatal("the dialog was laid out in a widget container")
	}
	if rows := viewRows(dialog); !strings.Contains(rows, "where") || !strings.Contains(rows, "Alpha") {
		t.Fatalf("the dialog draws %q", rows)
	}
	dialog.HandleInput("\x1b[B")
	dialog.HandleInput("\r")
	f.awaitResult()
	f.loop.run()
	if !slices.Equal(*picks, []string{`d {"value":"b"}`}) {
		t.Fatalf("the plugin was sent %q, want the dialog's second item", *picks)
	}
	if f.loop.drawn()[0] != panel || viewRows(panel) != under {
		t.Fatalf("the panel under the dialog changed: %q, want %q", viewRows(panel), under)
	}

	f.transition("hide", ref)
	if len(f.loop.open()) != 0 || f.loop.focused != panel || dialog.focused {
		t.Fatalf("after hide: %d frames, the panel focused %v", len(f.loop.open()), f.loop.focused == panel)
	}
	panel.HandleInput("\r")
	f.awaitResult()
	f.loop.run()
	if !slices.Equal(*picks, []string{`d {"value":"b"}`, `p {"value":"b"}`}) {
		t.Fatalf("the panel lost its selection under the dialog: %q", *picks)
	}
	f.transition("show", ref)
	if open := f.loop.open(); len(open) != 1 || open[0].mount != dialog || f.loop.focused != dialog {
		t.Fatal("show did not open the frame again with the focus")
	}
	if rows := viewRows(dialog); !strings.Contains(rows, "Beta") {
		t.Fatalf("the dialog shown again draws %q", rows)
	}
	f.transition("remove", ref)
	if len(f.loop.open()) != 0 || f.loop.focused != panel || len(f.loop.frames) != 2 {
		t.Fatalf("after remove: %d frames open of %d", len(f.loop.open()), len(f.loop.frames))
	}
}

// waitingRow is the row that stands for the dialogs, without styling, or "" while the client lays none.
func waitingRow(f *viewFixture) string {
	if f.loop.waiting == nil {
		return ""
	}
	return strings.TrimSpace(widthx.StripAnsi(strings.Join(f.loop.waiting.Render(40), "\n")))
}

// The key that interrupts cancels a cancellable intent of the dialog in flight and returns the focus to the editor. A
// dialog without the keyboard has no frame: one row names it. The focus key enters it again and leaves it for the
// editor, passing a focusable panel by; without a dialog it cycles the panels again.
func TestViewClientEscapeLeavesADialogForItsRow(t *testing.T) {
	f := newViewFixture(t)
	cancelled := make(chan struct{})
	f.host.invoke = func(ctx context.Context, _ uiview.Invocation) (uiview.Result, error) {
		<-ctx.Done()
		close(cancelled)
		return uiview.Result{}, ctx.Err()
	}
	f.mount("p", uiview.SlotPanel, uiview.Content{View: viewList("choose"), Intents: []uiview.IntentDecl{viewPickIntent}})
	panel := f.loop.drawn()[0]
	ref := f.mount("d", uiview.SlotDialog, viewDialogContent())
	dialog := f.loop.open()[0].mount
	if waitingRow(f) != "" {
		t.Fatalf("a dialog that holds the keyboard has the row %q", waitingRow(f))
	}
	dialog.HandleInput("\r")
	dialog.HandleInput("\x1b")
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Escape did not cancel the dialog's cancellable intent in flight")
	}
	if f.loop.focused != nil || dialog.focused || len(f.loop.open()) != 0 || waitingRow(f) != "d: F6 to enter" {
		t.Fatalf("after Escape: focused %v, frames %d, row %q", f.loop.focused, len(f.loop.open()), waitingRow(f))
	}
	f.awaitResult()
	f.loop.run()
	if len(f.loop.notices) != 0 {
		t.Fatalf("a cancelled intent wrote %q", f.loop.notices)
	}
	for i, entered := range []bool{true, false, true} {
		if !f.loop.key("\x1b[17~") || (f.loop.focused == dialog) != entered || len(f.loop.open()) != map[bool]int{true: 1, false: 0}[entered] || (waitingRow(f) == "") != entered {
			t.Fatalf("F6 press %d: the dialog focused %v, frames %d, row %q", i+1, f.loop.focused == dialog, len(f.loop.open()), waitingRow(f))
		}
		f.loop.run()
	}
	if panel.focused {
		t.Fatal("F6 stopped on the panel under the dialog")
	}
	f.transition("hide", ref)
	if f.loop.focused != nil || waitingRow(f) != "" || len(f.loop.open()) != 0 {
		t.Fatalf("the hidden dialog left the focus on %v, the row %q", f.loop.focused, waitingRow(f))
	}
	if !f.loop.key("\x1b[17~") || f.loop.focused != panel {
		t.Fatal("without a dialog F6 does not reach the panel")
	}
}

// Several live dialogs are stacked in the order they were shown: the last has the keyboard, and when it ends the one
// under it has it again. Leaving removes every frame and entering draws them all again. A dialog that ends under
// another takes nothing from the one on top.
func TestViewClientStacksDialogs(t *testing.T) {
	f := newViewFixture(t)
	first := f.mount("one", uiview.SlotDialog, viewDialogContent())
	second := f.mount("two", uiview.SlotDialog, viewDialogContent())
	open := f.loop.open()
	if len(open) != 2 || open[0].title != "one" || open[1].title != "two" || f.loop.focused != open[1].mount {
		t.Fatalf("two live dialogs: %d frames, the second focused %v", len(open), len(open) == 2 && f.loop.focused == open[1].mount)
	}
	one, two := open[0].mount, open[1].mount
	two.HandleInput("\x1b")
	if len(f.loop.open()) != 0 || waitingRow(f) != "two: F6 to enter" || f.loop.focused != nil {
		t.Fatalf("after leaving the top dialog: %d frames, row %q", len(f.loop.open()), waitingRow(f))
	}
	if !f.loop.key("\x1b[17~") || f.loop.focused != two {
		t.Fatal("F6 from the editor did not reach the dialog on top")
	}
	if open := f.loop.open(); len(open) != 2 || open[0].mount != one || open[1].mount != two {
		t.Fatalf("entering drew %d frames, want the stack of two in order", len(open))
	}
	f.transition("hide", second)
	if open := f.loop.open(); len(open) != 1 || open[0].mount != one || f.loop.focused != one {
		t.Fatal("the dialog under the one that ended does not have the keyboard")
	}
	f.transition("show", second)
	third := f.mount("three", uiview.SlotDialog, viewDialogContent())
	open = f.loop.open()
	if len(open) != 3 || open[1].mount != two || open[2].title != "three" {
		t.Fatalf("the stack after show and mount is %d frames", len(open))
	}
	three := open[2].mount
	f.transition("remove", first)
	if open := f.loop.open(); len(open) != 2 || f.loop.focused != three {
		t.Fatal("a dialog that ended under the top one moved the focus")
	}
	f.transition("finish", third)
	f.transition("remove", second)
	if len(f.loop.open()) != 0 || f.loop.focused != nil || len(f.loop.listeners) != 0 || waitingRow(f) != "" {
		t.Fatalf("after every dialog ended: %d frames, %d listeners, row %q", len(f.loop.open()), len(f.loop.listeners), waitingRow(f))
	}
}

// A dialog whose view has no node that takes keys still holds the focus, so no key reaches what is under it. One whose
// view the kit rejects is not drawn. A detach closes every frame.
func TestViewClientDialogWithoutAFocusableNodeAndDetach(t *testing.T) {
	f := newViewFixture(t)
	f.mount("plain", uiview.SlotDialog, uiview.Content{View: viewText("only text")})
	open := f.loop.open()
	if len(open) != 1 || f.loop.focused != open[0].mount || len(f.loop.listeners) != 1 {
		t.Fatalf("a dialog of text: %d frames, %d listeners", len(open), len(f.loop.listeners))
	}
	open[0].mount.HandleInput("x")
	if f.loop.focused != open[0].mount {
		t.Fatal("a key the dialog does not take moved the focus")
	}
	f.mount("bad", uiview.SlotDialog, uiview.Content{View: json.RawMessage(`{"root":{"kind":"select-list"}}`)})
	if len(f.loop.open()) != 1 || f.loop.focused != open[0].mount {
		t.Fatal("a dialog whose view the kit rejects was drawn")
	}
	f.client.detach()
	if len(f.loop.open()) != 0 || f.loop.focused != nil || len(f.loop.listeners) != 0 {
		t.Fatalf("after the detach: %d frames, %d listeners", len(f.loop.open()), len(f.loop.listeners))
	}
}

// A dialog enters by the rule of a focus request: not from an editor that holds text. Then it has no frame, so it
// covers nothing the user types: the row names it, the plugin reads an unfocused notice, and the focus key enters it.
func TestViewClientDialogTakesNoFocusFromAnEditorThatHoldsText(t *testing.T) {
	f := newViewFixture(t)
	f.loop.typed = true
	f.mount("d", uiview.SlotDialog, viewDialogContent())
	if len(f.loop.frames) != 0 || f.loop.focused != nil || waitingRow(f) != "d: F6 to enter" {
		t.Fatalf("a dialog shown while the editor holds text: %d frames, focused %v, row %q", len(f.loop.frames), f.loop.focused != nil, waitingRow(f))
	}
	var codes []string
	for _, n := range f.notices("d") {
		codes = append(codes, n.Code+" "+n.Path+" "+n.Message)
	}
	if !slices.Equal(codes, []string{"unfocused focus the editor holds text"}) {
		t.Fatalf("notices = %q", codes)
	}
	if !f.loop.key("\x1b[17~") || len(f.loop.open()) != 1 || f.loop.focused != f.loop.open()[0].mount || waitingRow(f) != "" {
		t.Fatal("F6 did not enter the dialog that waited on its row")
	}
}

// The frame is titled with the mount's title, or its key when it has none; a new title is a new frame, which keeps the
// focus. A key the dialog binds raises its intent while the dialog holds the focus, and the hint row of its named
// bindings is one of the dialog's rows.
func TestViewClientDialogTitleAndBindings(t *testing.T) {
	f := newViewFixture(t)
	picks := viewPicks(f)
	content := viewDialogContent()
	content.Title = "Map"
	content.Intents = append(content.Intents, uiview.IntentDecl{Name: "ext.close", Arg: uiview.Shape{Type: "object"}, TimeoutMs: 30000})
	content.Bindings = []uiview.Binding{{Key: "q", Intent: "ext.close", Name: "close"}}
	ref := f.mount("d", uiview.SlotDialog, content)
	open := f.loop.open()
	if len(open) != 1 || open[0].title != "Map" {
		t.Fatalf("the frame of a titled dialog: %d frames", len(open))
	}
	dialog := open[0].mount
	if rows := viewRows(dialog); !strings.HasSuffix(rows, "q close") {
		t.Fatalf("the focused dialog draws %q, want the hint row last", rows)
	}
	dialog.HandleInput("q")
	f.awaitResult()
	f.loop.run()
	if !slices.Equal(*picks, []string{"d {}"}) {
		t.Fatalf("the bound key sent the plugin %q", *picks)
	}
	if notices := f.notices("d"); len(notices) != 0 {
		t.Fatalf("a dialog whose intent a key raises left notices %v", notices)
	}
	content.Title = "Atlas"
	f.update(ref, content)
	open = f.loop.open()
	if len(open) != 1 || open[0].title != "Atlas" || open[0].mount != dialog || f.loop.focused != dialog || len(f.loop.frames) != 2 {
		t.Fatalf("after a new title: %d frames open of %d, focused %v", len(open), len(f.loop.frames), f.loop.focused == dialog)
	}
	dialog.HandleInput("\x1b")
	if rows := viewRows(dialog); strings.Contains(rows, "q close") {
		t.Fatalf("the dialog without the focus draws the hint row: %q", rows)
	}
}
