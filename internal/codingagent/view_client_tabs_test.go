package codingagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

const viewF6 = "\x1b[17~"

var viewOpenIntent = uiview.IntentDecl{Name: "ext.open", Arg: uiview.Shape{Type: "object"}, TimeoutMs: 30000, WhenHidden: true}

// press sends data as the terminal does: to the input listeners first, then to the component that holds the focus.
func (l *viewTestLoop) press(data string) {
	switch {
	case l.key(data):
	case l.onRow:
		l.row.HandleInput(data)
	case l.focused != nil:
		l.focused.HandleInput(data)
	}
}

// tabs is the tab row at 40 columns without styling, or "" while the client lays none.
func (l *viewTestLoop) tabs() string {
	if l.row == nil {
		return ""
	}
	return strings.TrimSpace(widthx.StripAnsi(strings.Join(l.row.Render(40), "\n")))
}

func (f *viewFixture) only() *viewMount {
	f.t.Helper()
	var panels []*viewMount
	for _, m := range f.loop.drawn() {
		if m.slot.slot == uiview.SlotPanel {
			panels = append(panels, m)
		}
	}
	if len(panels) != 1 {
		f.t.Fatalf("%d panels are drawn, want the front tab alone", len(panels))
	}
	return panels[0]
}

// pig additive (D614): the panels are tabs. One row lists the live panels and the hidden ones the user can open, by
// title or by key; only the front tab's view is drawn; a panel that is shown becomes the front tab; a single live panel
// without a title draws as it did before there were tabs.
func TestViewClientPanelsAreTabs(t *testing.T) {
	f := newViewFixture(t)
	refA := f.mount("a", uiview.SlotPanel, uiview.Content{View: viewText("alone")})
	if f.loop.row != nil || f.loop.tabs() != "" || len(f.loop.drawn()) != 1 || viewRows(f.only()) != "alone" {
		t.Fatalf("one live panel without a title: tab row %q, drawn %d", f.loop.tabs(), len(f.loop.drawn()))
	}
	refA = f.update(refA, uiview.Content{Title: "Mail", View: viewText("the inbox")})
	if got := f.loop.tabs(); got != "Mail" {
		t.Fatalf("tab row = %q, want Mail", got)
	}
	refB := f.mount("b", uiview.SlotPanel, uiview.Content{Title: "Calendar", View: viewText("the month")})
	if got, rows := f.loop.tabs(), viewRows(f.only()); got != "Mail  Calendar" || rows != "the month" {
		t.Fatalf("tab row %q drawing %q, want Mail  Calendar drawing the panel shown last", got, rows)
	}
	if row := strings.Join(f.loop.row.Render(40), ""); !strings.Contains(row, "\x1b[1m") || !strings.Contains(row, "\x1b[4m") {
		t.Fatalf("the front tab is not bold and underlined: %q", row)
	}
	// A hidden mount is listed only when it declares the intent a client raises to open it.
	refC := f.mount("c", uiview.SlotPanel, uiview.Content{Title: "Notes", View: viewText("the notes"), Intents: []uiview.IntentDecl{viewOpenIntent}})
	f.transition("hide", refC)
	refD := f.mount("d", uiview.SlotPanel, uiview.Content{Title: "Closed", View: viewText("closed")})
	f.transition("hide", refD)
	f.mount("key-only", uiview.SlotPanel, uiview.Content{View: viewText("no title")})
	if got, rows := f.loop.tabs(), viewRows(f.only()); got != "Mail  Calendar  (Notes)  key-only" || rows != "no title" {
		t.Fatalf("tab row %q drawing %q", got, rows)
	}
	// The front tab goes: the live tab before it takes its place when none follows.
	f.transition("hide", f.only().ref)
	if got, rows := f.loop.tabs(), viewRows(f.only()); got != "Mail  Calendar  (Notes)" || rows != "the month" {
		t.Fatalf("after the front tab was hidden: tab row %q drawing %q", got, rows)
	}
	f.transition("hide", refB)
	f.transition("show", refB)
	if rows := viewRows(f.only()); rows != "the month" {
		t.Fatalf("a panel that is shown again draws %q", rows)
	}
	f.transition("remove", refB)
	f.transition("remove", refC)
	f.transition("remove", refA)
	if f.loop.row != nil || len(f.loop.drawn()) != 0 {
		t.Fatalf("after the last listed panel ended the client still lays the tab row (%v) or %d mounts", f.loop.row != nil, len(f.loop.drawn()))
	}
}

// pig additive (D614): F6 walks the entries of the tab row and returns to the editor after the last. A live entry
// becomes the front tab and takes the focus when its view takes keys; the row holds the focus on any other entry. A
// hidden entry stays hidden under the walk: the key that confirms raises its whenHidden intent, once while it is in
// flight, and the handler's show brings it to the front with the focus.
func TestViewClientFocusKeyWalksTheTabs(t *testing.T) {
	f := newViewFixture(t)
	raised := make(chan string, 4)
	release := make(chan struct{})
	f.host.invoke = func(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
		raised <- inv.Intent + " " + string(inv.Arg)
		<-release
		if _, err := f.host.reg.Transition("ext", "show", uiview.RefArgs{Mount: inv.Mount}); err != nil {
			return uiview.Result{Outcome: uiview.OutcomeFailed, Error: err.Error()}, nil
		}
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	f.mount("a", uiview.SlotPanel, uiview.Content{Title: "Mail", View: viewList("inbox")})
	f.mount("b", uiview.SlotPanel, uiview.Content{Title: "Calendar", View: viewText("the month")})
	refC := f.mount("c", uiview.SlotPanel, uiview.Content{Title: "Notes", View: viewList("notes"), Intents: []uiview.IntentDecl{viewOpenIntent}})
	f.transition("hide", refC)
	a, b, c := f.client.order[0], f.client.order[1], f.client.order[2]
	if got := f.notices("c"); len(got) != 0 {
		t.Fatalf("the intent a hidden mount takes was reported as not offered: %+v", got)
	}
	if f.only() != b || f.loop.focused != nil || len(f.loop.listeners) != 1 {
		t.Fatal("setup: Calendar is not the front tab, or the focus key is not bound")
	}

	f.loop.press(viewF6)
	if f.only() != a || f.loop.focused != a || f.loop.tabs() != "[Mail]  Calendar  (Notes)" {
		t.Fatalf("first press: front %s, focused %v, row %q", f.only().key.key, f.loop.focused, f.loop.tabs())
	}
	f.loop.press(viewF6)
	if f.only() != b || f.loop.focused != nil || !f.loop.onRow || f.client.at != b || f.loop.tabs() != "Mail  [Calendar]  (Notes)" {
		t.Fatalf("second press: front %s, focused %v, on the row %v, row %q", f.only().key.key, f.loop.focused, f.loop.onRow, f.loop.tabs())
	}
	f.loop.press(viewF6)
	if f.only() != b || !f.loop.onRow || f.client.at != c || f.loop.tabs() != "Mail  Calendar  [(Notes)]  enter opens" {
		t.Fatalf("third press: front %s, on the row %v, row %q", f.only().key.key, f.loop.onRow, f.loop.tabs())
	}
	select {
	case got := <-raised:
		t.Fatalf("walking over a hidden entry raised %q", got)
	default:
	}
	f.loop.press("x")
	f.loop.press("\r")
	f.loop.press("\r")
	if got := <-raised; got != "ext.open {}" {
		t.Fatalf("the key that confirms raised %q, want ext.open {}", got)
	}
	close(release)
	f.awaitResult()
	f.loop.run()
	select {
	case got := <-raised:
		t.Fatalf("a second confirm while the intent was in flight raised %q", got)
	default:
	}
	if f.client.droppedActivations != 1 {
		t.Fatalf("dropped activations = %d, want the second confirm", f.client.droppedActivations)
	}
	if f.only() != c || f.loop.focused != c || f.loop.onRow || f.loop.tabs() != "Mail  Calendar  [Notes]" {
		t.Fatalf("after the handler showed the mount: front %s, focused %v, row %q", f.only().key.key, f.loop.focused, f.loop.tabs())
	}
	f.loop.press(viewF6)
	if f.loop.focused != nil || f.loop.onRow || f.only() != c || f.loop.tabs() != "Mail  Calendar  Notes" {
		t.Fatalf("after the last entry the focus is not the editor's, or the front tab changed: row %q", f.loop.tabs())
	}
	// The key that interrupts leaves the row for the editor.
	f.loop.press(viewF6)
	f.loop.press(viewF6)
	if !f.loop.onRow {
		t.Fatal("setup: the row does not hold the focus")
	}
	f.loop.press("\x1b")
	if f.loop.onRow || f.loop.focused != nil || f.client.at != nil || f.only() != b {
		t.Fatal("the key that interrupts did not return the focus from the row to the editor")
	}
	// A selector holds the focus: the key is passed on.
	f.loop.elsewhere = true
	if f.loop.key(viewF6) {
		t.Fatal("the focus key was consumed while a selector held the focus")
	}
}

// pig additive (D614): a focus request moves the focus only where it takes no key from the user. From an editor that
// holds no text it is granted; an editor that holds text keeps the focus, the view is shown all the same, and the
// plugin reads an unfocused notice. A request on an update names its node. A snapshot the client holds asks nothing
// again.
func TestViewClientGrantsAFocusRequestOnlyFromAnEmptyEditor(t *testing.T) {
	list := func(id string) json.RawMessage {
		return json.RawMessage(`{"root":{"kind":"vstack","children":[{"kind":"select-list","id":"top","maxVisible":2,"items":[{"value":"a","label":"Alpha"}]},` +
			`{"kind":"select-list","id":` + `"` + id + `"` + `,"maxVisible":2,"items":[{"value":"b","label":"Beta"}]}]}}`)
	}
	focus := func(request string) []uiview.Focus {
		return []uiview.Focus{{Node: "top"}, {Node: "reply", Request: request}}
	}
	unfocused := func(f *viewFixture, key string) (count int, message string) {
		for _, n := range f.notices(key) {
			if n.Code == uiview.NoticeUnfocused && n.Path == "focus" && n.Severity == uiview.SeverityInfo {
				count, message = count+n.Count, n.Message
			}
		}
		return count, message
	}

	f := newViewFixture(t)
	raised := make(chan string, 4)
	f.host.invoke = func(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
		raised <- inv.Intent
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	value := uiview.Shape{Type: "object", Properties: map[string]uiview.Shape{"value": {Type: "string"}}, Required: []string{"value"}}
	intents := []uiview.IntentDecl{{Name: "ext.top", NodeKey: "top", Arg: value, TimeoutMs: 30000}, {Name: "ext.reply", NodeKey: "reply", Arg: value, TimeoutMs: 30000}}
	// confirmed presses the key that confirms in the focused view and returns the intent its node raised.
	confirmed := func() string {
		t.Helper()
		f.loop.press("\r")
		got := <-raised
		f.awaitResult()
		f.loop.run()
		return got
	}
	ref := f.mount("a", uiview.SlotPanel, uiview.Content{Title: "Mail", View: list("reply"), Intents: intents, Focus: focus(uiview.FocusOnMount)})
	a := f.client.order[0]
	if f.loop.focused != a || a.node != "reply" {
		t.Fatalf("a request at mount with an empty editor: focused %v, node %q", f.loop.focused, a.node)
	}
	if got := confirmed(); got != "ext.reply" {
		t.Fatalf("the keys of the view went to the node of %s, want the requested node's ext.reply", got)
	}
	f.loop.press("\x1b")
	// The same revision again, as a resync sends it, asks nothing.
	f.client.resync(func() uiview.Resync { r, _ := f.client.att.Resync(); return r }())
	f.client.settle()
	if f.loop.focused != nil {
		t.Fatal("a snapshot the client already drew asked for the focus again")
	}
	ref = f.update(ref, uiview.Content{Title: "Mail", View: list("reply"), Intents: intents, Focus: focus("")})
	if f.loop.focused != nil || a.node != "reply" {
		t.Fatalf("an update without a request: focused %v, node %q", f.loop.focused, a.node)
	}
	// The view is the one already drawn: the request alone moves the keys to its node.
	ref = f.update(ref, uiview.Content{Title: "Mail", View: list("reply"), Intents: intents, Focus: []uiview.Focus{{Node: "top", Request: uiview.FocusOnUpdate}, {Node: "reply"}}})
	if f.loop.focused != a || a.node != "top" {
		t.Fatalf("a request on an update with an empty editor: focused %v, node %q", f.loop.focused, a.node)
	}
	if got := confirmed(); got != "ext.top" {
		t.Fatalf("the keys of the view went to the node of %s, want the requested node's ext.top", got)
	}
	if n, _ := unfocused(f, "a"); n != 0 {
		t.Fatalf("%d unfocused notices for granted requests", n)
	}

	// The editor holds text: the view is shown, the focus stays, the plugin is told.
	f.loop.press("\x1b")
	f.loop.typed = true
	f.mount("b", uiview.SlotPanel, uiview.Content{Title: "Calendar", View: list("reply"), Focus: focus(uiview.FocusOnMount)})
	if f.only() != f.client.order[1] || f.loop.focused != nil {
		t.Fatalf("a request at mount while the editor holds text: front %s, focused %v", f.only().key.key, f.loop.focused)
	}
	if n, why := unfocused(f, "b"); n != 1 || why != "the editor holds text" {
		t.Fatalf("unfocused notices of b = %d (%q), want 1: the editor holds text", n, why)
	}
	f.update(ref, uiview.Content{Title: "Mail", View: list("reply"), Intents: intents, Focus: focus(uiview.FocusOnUpdate)})
	if f.only() != a || f.loop.focused != nil {
		t.Fatalf("a request on an update while the editor holds text: front %s, focused %v", f.only().key.key, f.loop.focused)
	}
	if n, why := unfocused(f, "a"); n != 1 || why != "the editor holds text" {
		t.Fatalf("unfocused notices of a = %d (%q), want 1: the editor holds text", n, why)
	}
}

// pig additive (D614): a request never takes the drawn view from the one the user works in. With panel A focused,
// three updates of panel B that request the focus leave A drawn and focused, mark B's tab once, and leave one
// unfocused notice that counts three (the registry coalesces a repeated notice). On B's own tab the request is granted.
func TestViewClientRequestNeverTakesTheViewTheUserWorksIn(t *testing.T) {
	f := newViewFixture(t)
	f.mount("a", uiview.SlotPanel, uiview.Content{Title: "Mail", View: viewList("inbox")})
	refB := f.mount("b", uiview.SlotPanel, uiview.Content{Title: "Calendar", View: viewText("month 0")})
	a, b := f.client.order[0], f.client.order[1]
	f.loop.press(viewF6)
	if f.loop.focused != a || f.only() != a {
		t.Fatal("setup: Mail is not the focused front tab")
	}
	asking := func(i int) uiview.Content {
		return uiview.Content{Title: "Calendar", View: viewList("month " + string(rune('0'+i))), Focus: []uiview.Focus{{Node: "pick", Request: uiview.FocusOnUpdate}}}
	}
	for i := 1; i <= 3; i++ {
		refB = f.update(refB, asking(i))
		if f.loop.focused != a || f.only() != a || !strings.Contains(viewRows(f.only()), "inbox") {
			t.Fatalf("update %d of Calendar took the view or the focus from Mail: front %s, focused %v", i, f.only().key.key, f.loop.focused)
		}
		if got := f.loop.tabs(); got != "[Mail]  *Calendar" || !b.attention {
			t.Fatalf("update %d: tab row %q, want Calendar marked once", i, got)
		}
	}
	var notices []uiview.Notice
	for _, n := range f.notices("b") {
		if n.Code == uiview.NoticeUnfocused {
			notices = append(notices, n)
		}
	}
	if len(notices) != 1 || notices[0].Count != 3 || notices[0].Message != "another component holds the keyboard focus" {
		t.Fatalf("unfocused notices of Calendar = %+v, want one that counts 3", notices)
	}
	if row := strings.Join(f.loop.row.Render(40), ""); !strings.Contains(row, "*") {
		t.Fatalf("the mark is not drawn: %q", row)
	}
	// The user moves to Calendar: the mark goes, and a request made there is granted.
	f.loop.press(viewF6)
	if f.loop.focused != b || f.only() != b || b.attention || f.loop.tabs() != "Mail  [Calendar]" {
		t.Fatalf("after the user moved to Calendar: focused %v, row %q", f.loop.focused, f.loop.tabs())
	}
	f.update(refB, asking(4))
	if f.loop.focused != b {
		t.Fatal("a request of the view the user is in moved the focus away")
	}
	for _, n := range f.notices("b") {
		if n.Code == uiview.NoticeUnfocused && n.Count != 3 {
			t.Fatalf("a granted request left an unfocused notice: %+v", n)
		}
	}
}

// pig additive (D614): a label that has less room than its title is cut by terminal cells with the client's ellipsis,
// never inside a wide character, a combining sequence or an emoji sequence.
func TestViewClientCutsATabLabelByCells(t *testing.T) {
	family := "\U0001F468\u200d\U0001F469\u200d\U0001F467"
	// Each title is within the 64 bytes a title may have.
	for name, title := range map[string]string{
		"CJK":      strings.Repeat("\u90f5\u4fbf", 10),
		"emoji":    strings.Repeat(family, 3) + "xyz",
		"combined": strings.Repeat("e\u0301", 21),
	} {
		t.Run(name, func(t *testing.T) {
			f := newViewFixture(t)
			f.mount("a", uiview.SlotPanel, uiview.Content{Title: title, View: viewText("a")})
			f.mount("b", uiview.SlotPanel, uiview.Content{Title: "Calendar", View: viewText("b")})
			full, cuts := widthx.VisibleWidth(title), 0
			// 11 cells is the least a row of two labels takes: each label cut to its fewest cells.
			for width := 80; width >= 3+2*viewTabLabelMinCells; width-- {
				f.loop.row.Invalidate()
				rows := f.loop.row.Render(width)
				if len(rows) != 1 {
					t.Fatalf("the tab row is %d rows", len(rows))
				}
				row := widthx.StripAnsi(rows[0])
				if cells := widthx.VisibleWidth(row); cells > width || !utf8.ValidString(row) {
					t.Fatalf("at %d columns the row is %d cells (valid UTF-8: %v): %q", width, cells, utf8.ValidString(row), row)
				}
				label := strings.TrimSpace(strings.SplitN(row, "  ", 2)[0])
				kept, cut := strings.CutSuffix(label, viewTabEllipsis)
				switch {
				case !cut && label != title:
					t.Fatalf("at %d columns the label %q is neither the title nor cut with the ellipsis", width, label)
				case widthx.VisibleWidth(label) > viewTabLabelCells:
					t.Fatalf("at %d columns the label %q is %d cells, more than %d", width, label, widthx.VisibleWidth(label), viewTabLabelCells)
				case width == 80 && cut != (full > viewTabLabelCells):
					t.Fatalf("with room for it the label of a %d-cell title is %q", full, label)
				case !strings.HasPrefix(title, kept):
					t.Fatalf("at %d columns the label %q is no prefix of the title", width, label)
				case name == "emoji" && len(kept) < 3*len(family) && len(kept)%len(family) != 0:
					t.Fatalf("at %d columns the label %q ends inside an emoji sequence", width, label)
				case name == "combined" && strings.HasPrefix(title[len(kept):], "\u0301"):
					t.Fatalf("at %d columns the label %q ends between a letter and its combining mark", width, label)
				}
				if cut {
					cuts++
				}
			}
			if cuts == 0 {
				t.Fatal("no width cut the label: the test proves nothing")
			}
		})
	}
}

// pig additive (D614): a slot without tabs draws every live panel, one under the other, and lays no tab row: the path
// the tab row replaces stays whole behind the one column of viewSlots.
func TestViewClientStacksThePanelsOfASlotWithoutTabs(t *testing.T) {
	viewStackPanels(t)
	f := newViewFixture(t)
	f.mount("a", uiview.SlotPanel, uiview.Content{Title: "Mail", View: viewText("the inbox")})
	refB := f.mount("b", uiview.SlotPanel, uiview.Content{Title: "Calendar", View: viewList("the month"), Intents: []uiview.IntentDecl{viewOpenIntent}})
	if f.loop.row != nil || len(f.loop.drawn()) != 2 || viewRows(f.loop.drawn()[0]) != "the inbox" {
		t.Fatalf("stacked panels: tab row %v, %d drawn", f.loop.row != nil, len(f.loop.drawn()))
	}
	f.transition("hide", refB)
	if f.loop.row != nil || len(f.loop.drawn()) != 1 || len(f.loop.listeners) != 0 {
		t.Fatal("a hidden panel of a slot without tabs is listed")
	}
}
