package codingagent

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var (
	viewGoIntent = uiview.IntentDecl{
		Name: "ext.go", TimeoutMs: 30000,
		Arg: uiview.Shape{Type: "object", Properties: map[string]uiview.Shape{"n": {Type: "integer"}}},
	}
	viewTapIntent = uiview.IntentDecl{Name: "ext.tap", TimeoutMs: 30000, Arg: uiview.Shape{Type: "object"}}
)

func viewGo(key string, n int) uiview.Binding {
	return uiview.Binding{Key: key, Intent: "ext.go", Arg: json.RawMessage(`{"n":` + strconv.Itoa(n) + `}`)}
}

// raised makes the fixture's plugin record what it is asked, as "<mount> <intent> [<nodeKey>] <arg>".
func (f *viewFixture) raised() chan string {
	asked := make(chan string, 16)
	f.host.invoke = func(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
		asked <- inv.Mount.Key + " " + inv.Intent + " [" + inv.NodeKey + "] " + string(inv.Arg)
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	return asked
}

// fired presses data as the terminal does and returns what the plugin was asked, or "" when the key raised nothing.
func (f *viewFixture) fired(asked chan string, data string) string {
	f.t.Helper()
	f.loop.press(data)
	flying := false
	for _, m := range f.client.order {
		flying = flying || m.cancel != nil
	}
	if !flying {
		select {
		case got := <-asked:
			f.t.Fatalf("the plugin was asked %q with no intent in flight", got)
		default:
		}
		return ""
	}
	f.awaitResult()
	f.loop.run()
	return <-asked
}

// pig additive (D614): a key binding fires only while its mount holds the focus. A mount that binds a key is focusable
// as a whole, with no focus record and no node that takes keys, so the focus key reaches it. The binding's intent is
// raised with no node and the binding's argument, or {} when it has none. The key is compared as a parsed key: the
// plugin wrote ctrl before shift and the terminal reports shift before ctrl. A key no binding names is dropped, and a
// bound intent is not reported as one the terminal offers no control for.
func TestViewClientFiresABindingOnlyWhileItsMountHoldsTheFocus(t *testing.T) {
	f := newViewFixture(t)
	asked := f.raised()
	f.mount("p", uiview.SlotPanel, uiview.Content{
		View: viewText("the day"), Intents: []uiview.IntentDecl{viewGoIntent, viewTapIntent},
		Bindings: []uiview.Binding{viewGo("d", 1), {Key: "ctrl+shift+q", Intent: "ext.tap"}, viewGo("shift+left", -1)},
	})
	m := f.only()
	if f.loop.focused != nil || m.focused {
		t.Fatal("a mount that binds keys took the focus without a request")
	}
	if got := f.notices("p"); len(got) != 0 {
		t.Fatalf("intents a key is bound to were reported: %+v", got)
	}
	// The editor holds the focus: the key is the editor's, and the mount's own handler fires nothing either.
	if got := f.fired(asked, "d"); got != "" {
		t.Fatalf("with the editor focused, d raised %q", got)
	}
	m.HandleInput("d")
	if m.cancel != nil || f.client.flight != 0 {
		t.Fatal("a binding fired in a mount that does not hold the focus")
	}
	f.loop.press(viewF6)
	if f.loop.focused != m || !m.focused {
		t.Fatal("the focus key did not reach a mount whose only way to take keys is its bindings")
	}
	for _, c := range []struct{ name, data, want string }{
		{"a key with an argument", "d", `p ext.go [] {"n":1}`},
		{"a key without an argument, modifiers in the other order", "\x1b[113;6u", `p ext.tap [] {}`},
		{"a named key with a modifier", "\x1b[1;2D", `p ext.go [] {"n":-1}`},
		{"a key no binding names", "x", ""},
		{"the bound letter with shift", "D", ""},
		{"the bound named key without its modifier", "\x1b[D", ""},
	} {
		if got := f.fired(asked, c.data); got != c.want {
			t.Errorf("%s (%q) raised %q, want %q", c.name, c.data, got, c.want)
		}
	}
	// The key that interrupts leaves the mount, and its bindings with it.
	f.loop.press("\x1b")
	if f.loop.focused != nil {
		t.Fatal("the key that interrupts did not leave the mount")
	}
	if got := f.fired(asked, "d"); got != "" {
		t.Fatalf("after the focus left, d raised %q", got)
	}
}

// pig additive (D614): one intent per mount is in flight: a binding fired before the last intent settled is dropped and
// counted.
func TestViewClientDropsABindingWhileAnIntentIsInFlight(t *testing.T) {
	f := newViewFixture(t)
	release := make(chan struct{})
	calls := make(chan struct{}, 4)
	f.host.invoke = func(ctx context.Context, _ uiview.Invocation) (uiview.Result, error) {
		calls <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	f.mount("p", uiview.SlotPanel, uiview.Content{View: viewText("the day"), Intents: []uiview.IntentDecl{viewGoIntent}, Bindings: []uiview.Binding{viewGo("d", 1), viewGo("w", 2)}})
	f.loop.press(viewF6)
	f.loop.press("d")
	f.loop.press("w")
	f.loop.press("d")
	if f.client.droppedActivations != 2 {
		t.Fatalf("dropped activations = %d, want the two bindings fired while the first was in flight", f.client.droppedActivations)
	}
	close(release)
	f.awaitResult()
	f.loop.run()
	if len(calls) != 1 {
		t.Fatalf("three keys in one tick reached the plugin %d times", len(calls))
	}
	f.loop.press("w")
	f.awaitResult()
	f.loop.run()
	if len(calls) != 2 || f.client.droppedActivations != 2 {
		t.Fatalf("a binding fired after the first settled: plugin calls %d, dropped %d", len(calls), f.client.droppedActivations)
	}
}

// pig additive (D614): with the tab row, a key reaches the bindings of the mount whose view the user stands in. A live
// tab that binds a key takes the focus itself, so its own Enter binding fires; a tab without one leaves the focus on the
// row, where no binding is in force; and a hidden entry has none in force: Enter on it opens it, whatever it binds.
func TestViewClientBindingsFollowTheTabTheUserStandsOn(t *testing.T) {
	f := newViewFixture(t)
	asked := f.raised()
	f.mount("a", uiview.SlotPanel, uiview.Content{
		Title: "Day", View: viewText("the day"), Intents: []uiview.IntentDecl{viewGoIntent, viewTapIntent},
		Bindings: []uiview.Binding{viewGo("d", 1), {Key: "enter", Intent: "ext.tap"}},
	})
	f.mount("b", uiview.SlotPanel, uiview.Content{Title: "Week", View: viewText("the week")})
	refC := f.mount("c", uiview.SlotPanel, uiview.Content{
		Title: "Notes", View: viewText("the notes"), Intents: []uiview.IntentDecl{viewTapIntent, viewOpenIntent},
		Bindings: []uiview.Binding{{Key: "enter", Intent: "ext.tap"}, {Key: "d", Intent: "ext.tap"}},
	})
	f.transition("hide", refC)
	a, b, c := f.client.order[0], f.client.order[1], f.client.order[2]

	f.loop.press(viewF6)
	if f.loop.focused != a || f.loop.onRow || f.loop.tabs() != "[Day]  Week  (Notes)" {
		t.Fatalf("on the first tab: focused %v, on the row %v, row %q; want the mount that binds keys to hold the focus", f.loop.focused, f.loop.onRow, f.loop.tabs())
	}
	if got := f.fired(asked, "d"); got != `a ext.go [] {"n":1}` {
		t.Fatalf("d on the tab the user stands on raised %q", got)
	}
	if got := f.fired(asked, "\r"); got != `a ext.tap [] {}` {
		t.Fatalf("Enter, bound by the tab the user stands on, raised %q", got)
	}
	f.loop.press(viewF6)
	if !f.loop.onRow || f.client.at != b {
		t.Fatal("setup: the row does not stand on the second tab")
	}
	for _, key := range []string{"d", "\r"} {
		if got := f.fired(asked, key); got != "" {
			t.Fatalf("%q on a tab without bindings raised %q: a binding of another mount fired", key, got)
		}
	}
	f.loop.press(viewF6)
	if !f.loop.onRow || f.client.at != c {
		t.Fatal("setup: the row does not stand on the hidden entry")
	}
	if got := f.fired(asked, "d"); got != "" {
		t.Fatalf("d on a hidden entry raised %q: a hidden mount has no binding in force", got)
	}
	if got := f.fired(asked, "\r"); got != `c ext.open [] {}` {
		t.Fatalf("Enter on a hidden entry raised %q, want the intent that opens it", got)
	}
	f.loop.press("\x1b")
	if f.loop.onRow || f.loop.focused != nil {
		t.Fatal("the key that interrupts did not leave the tab row")
	}
}

// pig additive (D614): the focused node has the key first. A list takes Down, so a binding of Down never fires while the
// list holds the keys; an input takes a printable key, so the letter is typed and its binding does not fire. A key the
// list declines reaches the bindings. (The view kit's input and editor nodes report every key as taken, so while one
// holds the keys of a view no binding of that view fires: that is the kit's answer, not a rule of the client.)
func TestViewClientFocusedNodeTakesAKeyBeforeABinding(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		f := newViewFixture(t)
		asked := f.raised()
		f.mount("p", uiview.SlotPanel, uiview.Content{
			View: viewList("choose"), Intents: []uiview.IntentDecl{viewPickIntent, viewGoIntent, viewTapIntent},
			Focus:    []uiview.Focus{{Node: "pick", Request: "mount"}},
			Bindings: []uiview.Binding{{Key: "down", Intent: "ext.tap"}, viewGo("d", 1)},
		})
		if f.loop.focused != f.only() {
			t.Fatal("setup: the panel does not hold the focus")
		}
		if got := f.fired(asked, "\x1b[B"); got != "" {
			t.Fatalf("Down, which the list takes, raised %q", got)
		}
		if got := f.fired(asked, "\r"); got != `p ext.pick [pick] {"value":"b"}` {
			t.Fatalf("after Down, Enter raised %q: the list did not get the key", got)
		}
		if got := f.fired(asked, "d"); got != `p ext.go [] {"n":1}` {
			t.Fatalf("d, which the list declines, raised %q", got)
		}
	})
	t.Run("input", func(t *testing.T) {
		f := newViewFixture(t)
		asked := f.raised()
		f.mount("p", uiview.SlotPanel, uiview.Content{
			View:    json.RawMessage(`{"root":{"kind":"input","id":"name"},"focus":"name"}`),
			Intents: []uiview.IntentDecl{viewGoIntent, viewTapIntent}, Focus: []uiview.Focus{{Node: "name", Request: "mount"}},
			Bindings: []uiview.Binding{viewGo("d", 1)},
		})
		m := f.only()
		if f.loop.focused != m {
			t.Fatal("setup: the panel does not hold the focus")
		}
		if got := f.fired(asked, "d"); got != "" {
			t.Fatalf("d, typed into the input, raised %q", got)
		}
		if rows := viewRows(m); !strings.Contains(rows, "d") {
			t.Fatalf("the input does not hold the typed letter: %q", rows)
		}
	})
}

// viewBoundMount is the arguments of a mount, as an extension sends them, that binds %KEY%.
const viewBoundMount = `{"key":"bound","slot":"panel","view":{"root":{"kind":"text","text":"x"}},` +
	`"intents":[{"name":"ext.tap","arg":{"type":"object"}}],"bindings":[{"key":"%KEY%","intent":"ext.tap"}]}`

// pig additive (D614): every key the terminal client keeps for itself is in the table of reserved keys, so no binding
// can name it: the focus key, the default key that interrupts and Tab. The host refuses a mount that binds a row, and
// in a focused mount that binds other keys a reserved key raises nothing and does what the client keeps it for. Enter
// on a tab entry is kept only while the row holds the focus, where no binding is in force
// (TestViewClientBindingsFollowTheTabTheUserStandsOn), so it needs no row.
func TestViewClientKeepsOnlyReservedKeys(t *testing.T) {
	kept := []string{viewFocusKey, "tab"}
	for i := range appKeybindingRows {
		if row := &appKeybindingRows[i]; row.id == string(AppInterrupt) {
			if len(row.keys) == 0 {
				t.Fatal("the key that interrupts has no default: the enumeration is empty")
			}
			for _, key := range row.keys {
				kept = append(kept, string(key))
			}
		}
	}
	for _, id := range kept {
		if key, err := uiview.ParseBindingKey(id); err != nil || key.Reserved() == nil {
			t.Errorf("the client keeps %q, which is not reserved (%v): an extension could bind it and never be given it", id, err)
		}
	}
	f := newViewFixture(t)
	asked := f.raised()
	for _, row := range uiview.ReservedKeys() {
		id := row.Base
		if row.Ctrl {
			id = "ctrl+" + id
		}
		if _, err := uiview.DecodeMount([]byte(strings.Replace(viewBoundMount, "%KEY%", id, 1))); err == nil || err.Code != uiview.CodeInvalid {
			t.Fatalf("a mount that binds the reserved key %q decoded with %v, want invalid", id, err)
		}
	}
	if _, err := uiview.DecodeMount([]byte(strings.Replace(viewBoundMount, "%KEY%", "d", 1))); err != nil {
		t.Fatalf("the same mount with a key that is not reserved: %v", err)
	}
	f.mount("p", uiview.SlotPanel, uiview.Content{View: viewText("the day"), Intents: []uiview.IntentDecl{viewGoIntent}, Bindings: []uiview.Binding{viewGo("d", 1), viewGo("i", 2), viewGo("c", 3)}})
	m := f.only()
	f.loop.press(viewF6)
	for _, data := range []string{"\t", "\x1b[Z", "\x03", "\x04", "\x1a"} {
		if got := f.fired(asked, data); got != "" || f.loop.focused != m {
			t.Fatalf("the reserved key %q raised %q, or moved the focus", data, got)
		}
	}
	if got := f.fired(asked, "\x1b"); got != "" || f.loop.focused != nil {
		t.Fatalf("the key that interrupts raised %q, or did not leave the mount", got)
	}
	if got := f.fired(asked, viewF6); got != "" || f.loop.focused != m {
		t.Fatalf("the focus key raised %q, or did not enter the mount", got)
	}
	if got := f.fired(asked, viewF6); got != "" || f.loop.focused != nil {
		t.Fatalf("the focus key raised %q, or did not leave the mount", got)
	}
}

// pig additive (D614): a binding the terminal cannot honour is told to the plugin once, as an unsupported notice of
// severity info with the path bindings[i]: a gesture, which the terminal does not report, and a key of a mount in a slot
// that never holds the focus. Drawing frames, the focus and a further revision with the same bindings report nothing
// again.
func TestViewClientReportsABindingItCannotHonourOnce(t *testing.T) {
	f := newViewFixture(t)
	content := func(text string) uiview.Content {
		return uiview.Content{
			View: viewText(text), Intents: []uiview.IntentDecl{viewTapIntent},
			Bindings: []uiview.Binding{{Key: "d", Intent: "ext.tap", Name: "Day"}, {Gesture: "swipe-left", Intent: "ext.tap"}},
		}
	}
	ref := f.mount("p", uiview.SlotPanel, content("one"))
	f.mount("w", uiview.SlotWidgetAbove, uiview.Content{View: viewText("widget"), Intents: []uiview.IntentDecl{viewTapIntent}, Bindings: []uiview.Binding{{Key: "d", Intent: "ext.tap"}}})
	m := f.only()
	for range 5 {
		m.Render(40)
		m.Invalidate()
	}
	f.loop.press(viewF6)
	m.Render(40)
	f.update(ref, content("two"))
	m.Render(40)
	check := func(key, path string, others int) {
		t.Helper()
		found, rest := 0, 0
		for _, n := range f.notices(key) {
			if n.Code == string(uiview.CodeUnsupported) && n.Severity == uiview.SeverityInfo && n.Path == path {
				found++
				if n.Count > 1 {
					t.Errorf("mount %s: %s was reported %d times", key, path, n.Count)
				}
			} else {
				rest++
			}
		}
		if found != 1 || rest != others {
			t.Errorf("mount %s: %d notices for %s and %d others, want 1 and %d: %+v", key, found, path, rest, others, f.notices(key))
		}
	}
	check("p", "bindings[1]", 0)
	// The widget's intent has no control in the terminal either, which is the notice the client always wrote.
	check("w", "bindings[0]", 1)
	if w := f.client.order[1]; w.takesFocus() || w.keys {
		t.Fatal("a mount of a widget slot offers a key")
	}
}

// pig additive (D614): while a mount holds the focus, one row under its view lists its named key bindings, key then
// name, in the theme's muted colour. The row is cut by cells, a name is written on one line, the row counts against the
// slot's rows, and a mount without a named key binding draws the rows it drew before bindings existed.
func TestViewClientDrawsAHintRowForTheFocusedMount(t *testing.T) {
	theme := tui.ActiveTheme()
	last := func(t *testing.T, lines []string) string {
		t.Helper()
		if len(lines) == 0 {
			t.Fatal("the mount draws no row")
		}
		return lines[len(lines)-1]
	}
	t.Run("named", func(t *testing.T) {
		f := newViewFixture(t)
		content := func(view json.RawMessage) uiview.Content {
			return uiview.Content{View: view, Intents: []uiview.IntentDecl{viewGoIntent, viewTapIntent}, Bindings: []uiview.Binding{
				{Key: "d", Intent: "ext.tap", Name: "Day"}, {Key: "x", Intent: "ext.go"}, {Key: "shift+left", Intent: "ext.go", Arg: json.RawMessage(`{"n":2}`), Name: "Back"},
				{Gesture: "swipe-up", Intent: "ext.tap", Name: "Swipe"}, {Key: "m", Intent: "ext.go", Arg: json.RawMessage(`{"n":3}`), Name: "Mo\x1b[31mnth\nview"},
			}}
		}
		ref := f.mount("p", uiview.SlotPanel, content(viewText("the day")))
		m := f.only()
		before := strings.Join(m.Render(40), "\n")
		if rows := viewRows(m); rows != "the day" {
			t.Fatalf("without the focus the mount draws %q", rows)
		}
		f.loop.press(viewF6)
		const hint = "d Day  shift+left Back  m Mo [31mnth view"
		lines := m.Render(60)
		if got, want := last(t, lines), " "+theme.Fg("muted", hint); got != want {
			t.Fatalf("the hint row is %q, want %q", got, want)
		}
		if len(lines) != len(strings.Split(before, "\n"))+1 {
			t.Fatalf("the focused mount draws %d rows, want the view's and one hint row", len(lines))
		}
		if got, want := last(t, m.Render(12)), " "+theme.Fg("muted", widthx.TruncateToWidth(hint, 11, viewTabEllipsis, false)); got != want || widthx.VisibleWidth(got) > 12 {
			t.Fatalf("at 12 columns the hint row is %q (%d cells), want %q", got, widthx.VisibleWidth(got), want)
		}
		// The row is one of the slot's rows: a view taller than the slot is cut one row earlier.
		tall := viewText(strings.Repeat("row\n", 29) + "row")
		f.update(ref, content(tall))
		limit := viewPanelSlotRows(f.loop.size.rows)
		lines = m.Render(60)
		if len(lines) != limit || !strings.Contains(lines[limit-2], viewCutRow) || !strings.Contains(last(t, lines), "d Day") {
			t.Fatalf("a tall focused view draws %d rows, ending %q; want %d with the cut row and the hint row last", len(lines), lines[max(len(lines)-2, 0):], limit)
		}
		f.loop.press("\x1b")
		lines = m.Render(60)
		if len(lines) != limit || !strings.Contains(last(t, lines), viewCutRow) {
			t.Fatalf("without the focus the tall view draws %d rows ending %q", len(lines), last(t, lines))
		}
	})
	t.Run("cells", func(t *testing.T) {
		f := newViewFixture(t)
		f.mount("p", uiview.SlotPanel, uiview.Content{View: viewText("x"), Intents: []uiview.IntentDecl{viewTapIntent}, Bindings: []uiview.Binding{{Key: "d", Intent: "ext.tap", Name: "日本語のビューを開く"}}})
		m := f.only()
		f.loop.press(viewF6)
		for width := 1; width <= 26; width++ {
			row := widthx.StripAnsi(last(t, m.Render(width)))
			if !utf8.ValidString(row) || widthx.VisibleWidth(row) > width {
				t.Fatalf("at %d columns the hint row is %q (%d cells)", width, row, widthx.VisibleWidth(row))
			}
		}
	})
	t.Run("unnamed", func(t *testing.T) {
		f := newViewFixture(t)
		f.mount("p", uiview.SlotPanel, uiview.Content{View: viewText("the day"), Intents: []uiview.IntentDecl{viewTapIntent}, Bindings: []uiview.Binding{{Key: "d", Intent: "ext.tap"}, {Gesture: "swipe-up", Intent: "ext.tap", Name: "Swipe"}}})
		m := f.only()
		before := strings.Join(m.Render(40), "\n")
		f.loop.press(viewF6)
		if f.loop.focused != m {
			t.Fatal("setup: the mount does not hold the focus")
		}
		if after := strings.Join(m.Render(40), "\n"); after != before || before == "" {
			t.Fatalf("a focused mount without a named key binding draws\n%q\nand without the focus\n%q", after, before)
		}
	})
}

// pig additive (D614): how a key the terminal reports meets a binding, over the legacy encoding, the kitty keyboard
// protocol as PiG enables it (a key that produces text arrives as that text), a terminal that reports every key as an
// escape code, and xterm's modifyOtherKeys. A symbol is bound by the character it produces: the terminal says which
// character that is in the text, or in the shifted alternate of an escape code. What the report does not say is not
// guessed:
//   - an escape code that names the base key and shift without the shifted alternate (shift and "/", shift and "ß")
//     fires neither the binding of the base key nor one of a symbol: which symbol the layout produces is not in it;
//   - a legacy "A" is shift and "a" or Caps Lock and "a": it fires shift+a (kitty reports Caps Lock and fires a);
//   - a digit produced with shift (AZERTY) fires as that digit when it arrives as text, and not from an escape code;
//   - "+" cannot be bound: it separates a modifier from its key, so the registry refuses the id.
func TestViewClientBindingKeyTable(t *testing.T) {
	was := tui.IsKittyProtocolActive()
	defer tui.SetKittyProtocolActive(was)
	keys := []string{"?", ":", "shift+a", "a", "/", "ctrl+?", "alt+d", "1", "="}
	f := newViewFixture(t)
	asked := f.raised()
	var bindings []uiview.Binding
	for i, key := range keys {
		bindings = append(bindings, viewGo(key, i))
	}
	f.mount("p", uiview.SlotPanel, uiview.Content{View: viewText("keys"), Intents: []uiview.IntentDecl{viewGoIntent}, Bindings: bindings})
	f.loop.press(viewF6)
	if _, err := uiview.ParseBindingKey("+"); err == nil {
		t.Error(`the key id "+" parsed: the table below says it cannot be bound`)
	}
	for _, c := range []struct {
		name  string
		kitty bool
		data  string
		want  string
	}{
		{"legacy ?", false, "?", "?"},
		{"legacy :", false, ":", ":"},
		{"legacy A", false, "A", "shift+a"},
		{"legacy a", false, "a", "a"},
		{"legacy /", false, "/", "/"},
		{"legacy + is bound by nothing", false, "+", ""},
		{"legacy alt+d", false, "\x1bd", "alt+d"},
		{"kitty text ?", true, "?", "?"},
		{"kitty text :", true, ":", ":"},
		{"kitty text A", true, "A", "shift+a"},
		{"kitty text 1 (AZERTY shift and &)", true, "1", "1"},
		{"kitty Caps Lock and a", true, "\x1b[97;65u", "a"},
		{"every key as an escape code: shift and / with the alternate ?", true, "\x1b[47:63;2u", "?"},
		{"every key as an escape code: shift and ; with the alternate :", true, "\x1b[59:58;2u", ":"},
		{"every key as an escape code: shift and = with the alternate +", true, "\x1b[61:43;2u", ""},
		{"every key as an escape code: shift and a with the alternate A", true, "\x1b[97:65;2u", "shift+a"},
		{"every key as an escape code: shift and a without an alternate", true, "\x1b[97;2u", "shift+a"},
		{"every key as an escape code: shift and / without an alternate", true, "\x1b[47;2u", ""},
		{"every key as an escape code: shift and & with the alternate 1 (AZERTY)", true, "\x1b[38:49;2u", ""},
		{"kitty ctrl, shift and / with the alternate ?", true, "\x1b[47:63;6u", "ctrl+?"},
		{"kitty ctrl, shift and / without an alternate", true, "\x1b[47;6u", ""},
		{"German layout: shift and ß with the alternate ?", true, "\x1b[223:63;2u", "?"},
		{"German layout: shift and ß without an alternate", true, "\x1b[223;2u", ""},
		{"Cyrillic layout: alt and в, whose base layout key is d", true, "\x1b[1074::100;3u", "alt+d"},
		{"modifyOtherKeys shift and ?", false, "\x1b[27;2;63~", "?"},
		{"modifyOtherKeys shift and :", false, "\x1b[27;2;58~", ":"},
	} {
		tui.SetKittyProtocolActive(c.kitty)
		want := ""
		if c.want != "" {
			want = `p ext.go [] {"n":` + strconv.Itoa(slices.Index(keys, c.want)) + `}`
		}
		if got := f.fired(asked, c.data); got != want {
			t.Errorf("%s (%q) raised %q, want %q (the binding of %q)", c.name, c.data, got, want, c.want)
		}
	}
}

const viewProbeBound = `{"key":"day","slot":"panel","view":{"root":{"kind":"text","text":"the day rows"}},` +
	`"intents":[{"name":"ext.go","arg":{"type":"object","properties":{"n":{"type":"integer"}}}}],` +
	`"bindings":[{"key":"d","intent":"ext.go","arg":{"n":1},"name":"Day"},{"key":"w","intent":"ext.go"},{"gesture":"swipe-left","intent":"ext.go"}]}`

// pig additive (D614): bindings on the production renderer, in both TUI modes. Before the mount holds the focus its key
// is the editor's and no hint row is drawn; F6 gives the focus to a mount whose view takes no key, the hint row is
// drawn, the key raises the intent and never reaches the editor; the key that interrupts returns the focus and the
// hint row goes. The gesture is in the extension's diagnostics once.
func TestTerminalViewClientFiresBindingsFromTheFocusedMount(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := newViewModeProbe(t, mode, true)
				p.must("ui.view.mount", viewProbeBound)
				if frame := widthx.StripAnsi(p.frame()); !strings.Contains(frame, "the day rows") || strings.Contains(frame, "d Day") {
					t.Fatalf("the mount drew %q, want its view and no hint row", frame)
				}
				client := p.m.viewClient.Load()
				mount := client.order[0]
				rows := func() string { return widthx.StripAnsi(strings.Join(p.m.widgetContainer.Render(80), "\n")) }
				if err := p.m.handleKey(t.Context(), "d"); err != nil {
					t.Fatal(err)
				}
				if text := p.m.editor.Text(); text != "d" || client.flight != 0 {
					t.Fatalf("with the editor focused, d left the editor holding %q and raised %d intents", text, client.flight)
				}
				p.typeText("\x7f")
				if _, consumed := p.m.tuiInst.RunInputListeners("\x1b[17~"); !consumed || p.m.tuiInst.GetFocusedComponent() != tui.Component(mount) {
					t.Fatalf("F6 gave the focus to %T, want the mount that binds keys", p.m.tuiInst.GetFocusedComponent())
				}
				if frame := widthx.StripAnsi(p.frame()); !strings.Contains(frame, "d Day") || !strings.Contains(rows(), "d Day") {
					t.Fatalf("the focused mount drew %q, want the hint row", frame)
				}
				if err := p.m.handleKey(t.Context(), "d"); err != nil {
					t.Fatal(err)
				}
				if text := p.m.editor.Text(); text != "" || client.flight != 1 {
					t.Fatalf("d in the focused mount left the editor holding %q and raised %d intents, want none and 1", text, client.flight)
				}
				p.frame()
				if err := p.m.handleKey(t.Context(), "\x1b"); err != nil {
					t.Fatal(err)
				}
				p.frame()
				if p.m.tuiInst.GetFocusedComponent() != tui.Component(p.m.editor) || strings.Contains(rows(), "d Day") || !strings.Contains(rows(), "the day rows") {
					t.Fatalf("after the key that interrupts: focus %T, rows %q", p.m.tuiInst.GetFocusedComponent(), rows())
				}
				var diag struct {
					Notices []uiview.Notice `json:"notices"`
				}
				res, err := p.bridge.HandleCall("ext", &subprocess.CallPayload{Method: "ui.view.diagnostics", Args: json.RawMessage(`{"key":"day"}`)})
				if err != nil || res.Error != nil || json.Unmarshal(res.Result, &diag) != nil {
					t.Fatalf("diagnostics: %v %+v", err, res)
				}
				gestures := 0
				for _, n := range diag.Notices {
					if n.Code == string(uiview.CodeUnsupported) && n.Severity == uiview.SeverityInfo && n.Path == "bindings[2]" && n.Count <= 1 {
						gestures++
					}
				}
				if gestures != 1 {
					t.Fatalf("the extension reads %+v, want the gesture reported once", diag.Notices)
				}
			})
		})
	}
}

// pig additive (D614): a mount whose bindings have no name is written to the terminal byte for byte as the same mount
// without bindings: when it is mounted, when F6 enters it and for a key its list takes.
func TestTerminalViewClientUnnamedBindingsDrawAsNone(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				run := func(extra string) []string {
					p := newViewModeProbe(t, mode, true)
					var frames []string
					p.must("ui.view.mount", `{"key":"list","slot":"panel"`+extra+`,"view":`+viewProbeList+`}`)
					frames = append(frames, p.frame())
					if _, consumed := p.m.tuiInst.RunInputListeners("\x1b[17~"); !consumed {
						t.Fatal("F6 was not consumed")
					}
					frames = append(frames, p.frame())
					if err := p.m.handleKey(t.Context(), "\x1b[B"); err != nil {
						t.Fatal(err)
					}
					frames = append(frames, p.frame())
					p.m.detachTerminalViewClient()
					return frames
				}
				plain := run("")
				bound := run(`,"intents":[{"name":"ext.go","arg":{"type":"object"}}],"bindings":[{"key":"d","intent":"ext.go"},{"gesture":"swipe-up","intent":"ext.go","name":"Up"}]`)
				for i := range plain {
					if bound[i] != plain[i] {
						t.Fatalf("frame %d with unnamed bindings wrote\n%q\nand without bindings\n%q", i, bound[i], plain[i])
					}
					if plain[i] == "" {
						t.Fatalf("frame %d is empty: the comparison proves nothing", i)
					}
				}
			})
		})
	}
}
