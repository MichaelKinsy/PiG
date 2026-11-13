package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// gridView is a grid of n cells in seven columns, each cell "d<i>" with chips chips of the given text, as a mount's view.
func gridView(n, chips int, chipText string) json.RawMessage {
	var cells []string
	for i := range n {
		var cs []string
		for c := range chips {
			cs = append(cs, fmt.Sprintf(`{"text":%q,"color":"#3b82f6"}`, fmt.Sprintf("%s %d", chipText, c)))
		}
		cells = append(cells, fmt.Sprintf(`{"value":"d%d","label":"%d","chips":[%s]}`, i, i+1, strings.Join(cs, ",")))
	}
	return json.RawMessage(`{"root":{"kind":"grid","id":"cal","columns":7,"head":["Mon","Tue","Wed","Thu","Fri","Sat","Sun"],"selected":"d3","cells":[` + strings.Join(cells, ",") + `]},"focus":"cal"}`)
}

var gridPickIntent = uiview.IntentDecl{
	Name: "ext.day", NodeKey: "cal", TimeoutMs: 30000,
	Arg: uiview.Shape{Type: "object", Properties: map[string]uiview.Shape{"value": {Type: "string"}}, Required: []string{"value"}},
}

// pig additive (D614): a focused grid takes the arrow keys, Home, End, Enter and Space, and no other key and no move past
// an edge, which go to the client's unhandled-key path; Enter raises the intent of the node with the cell's value, and
// only while the grid holds the focus.
func TestViewClientGridKeysRaiseTheCellsValue(t *testing.T) {
	f := newViewFixture(t)
	args := make(chan string, 4)
	f.host.invoke = func(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
		args <- inv.NodeKey + " " + inv.Intent + " " + string(inv.Arg)
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	f.mount("cal", uiview.SlotPanel, uiview.Content{View: gridView(14, 1, "x"), Intents: []uiview.IntentDecl{gridPickIntent}, Focus: []uiview.Focus{{Node: "cal", Request: "mount"}}})
	m := f.loop.drawn()[0]
	if f.loop.focused != m || !m.takesFocus() {
		t.Fatal("a panel whose focus record names the grid did not take the focus")
	}
	enter := func() string {
		t.Helper()
		m.HandleInput("\r")
		select {
		case got := <-args:
			f.awaitResult()
			f.loop.run()
			return got
		case <-time.After(5 * time.Second):
			t.Fatal("Enter raised no intent")
			return ""
		}
	}
	if got := enter(); got != `cal ext.day {"value":"d3"}` {
		t.Fatalf("Enter on the selected cell raised %q", got)
	}
	for _, step := range []struct {
		key, want string
		took      bool
	}{
		{"\x1b[C", "d4", true}, {"\x1b[B", "d11", true}, {"\x1b[A", "d4", true}, {"\x1b[D", "d3", true},
		{"d", "d3", false}, {"\x1b[1;5C", "d3", false}, {"\x1b[B", "d10", true}, {"\x1b[B", "d10", false}, {"\x1b[F", "d13", true},
		{"\x1b[C", "d13", false}, {"\x1b[H", "d7", true}, {"\x1b[H", "d7", false},
	} {
		if took := m.surface.HandleInput(step.key); took != step.took {
			t.Fatalf("key %q: taken %v, want %v", step.key, took, step.took)
		}
		if step.took {
			if got := enter(); !strings.HasSuffix(got, `{"value":"`+step.want+`"}`) {
				t.Fatalf("after key %q Enter raised %q, want the value %s", step.key, got, step.want)
			}
		}
	}
	// An unfocused panel is not sent the keys: the client's own key path takes none.
	f.loop.ui().focus(nil)
	if f.loop.key("\r") || len(args) != 0 {
		t.Fatalf("Enter without the focus reached the grid (%d intents)", len(args))
	}
	// The cursor is kept by value across an update that keeps its cell.
	f.update(m.ref, uiview.Content{View: gridView(14, 2, "y"), Intents: []uiview.IntentDecl{gridPickIntent}})
	f.loop.ui().focus(m)
	if got := enter(); got != `cal ext.day {"value":"d7"}` {
		t.Fatalf("after an update the cursor is on %q, want d7", got)
	}
}

// pig additive (D614): a press on a cell moves the cursor and focuses a panel, and the click raises the intent with the
// cell's value; a mount of a widget slot takes the pointer and never the focus.
func TestViewClientGridClickRaisesTheCellsValue(t *testing.T) {
	f := newViewFixture(t)
	args := make(chan string, 4)
	f.host.invoke = func(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
		args <- string(inv.Arg)
		return uiview.Result{Outcome: uiview.OutcomeDone}, nil
	}
	f.mount("w", uiview.SlotWidgetAbove, uiview.Content{View: gridView(14, 1, "x"), Intents: []uiview.IntentDecl{gridPickIntent}})
	f.mount("p", uiview.SlotPanel, uiview.Content{View: gridView(14, 1, "x"), Intents: []uiview.IntentDecl{gridPickIntent}})
	for _, m := range f.loop.drawn() {
		rows := m.Render(40)
		// 40 columns: cells of 4 and a separator; the head is line 0 and the first week starts on line 1.
		x, y := 3*5+1, 1
		_ = rows
		event := tui.TuiMouseEvent{Type: tui.MousePress, Button: tui.MouseButtonLeft, X: x, Y: y, ScreenX: x, ScreenY: y, Width: 40, Height: len(rows)}
		pressed := m.HandleMouse(event)
		if pressed == nil {
			t.Fatal("the press was not taken")
		}
		if pressed.Focus != m.slot.focus {
			t.Fatalf("slot %s: press asks for the focus: %v", m.slot.slot, pressed.Focus)
		}
		event.Type, event.ClickCount = tui.MouseClick, 1
		m.HandleMouse(event)
		select {
		case got := <-args:
			if got != `{"value":"d3"}` {
				t.Fatalf("the click raised %s, want the cell d3", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the click raised no intent")
		}
		f.awaitResult()
		f.loop.run()
		// A click between two columns, on the separator, chooses nothing.
		event.X = 4
		m.HandleMouse(event)
		select {
		case got := <-args:
			t.Fatalf("a click on a separator raised %s", got)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// pig additive (D614): at 40, 80 and 120 columns every row of a mounted month is exactly the terminal's width, in a tall
// terminal and in a short one, where the grid draws fewer chip lines instead of being cut.
func TestViewClientGridRowsAreExactlyTheWidth(t *testing.T) {
	for _, rows := range []int{100, 24} {
		for _, width := range []int{40, 80, 120} {
			t.Run(fmt.Sprintf("%dx%d", width, rows), func(t *testing.T) {
				f := newViewFixture(t)
				f.loop.size = viewSize{columns: width, rows: rows}
				f.client.declared = f.loop.size
				f.mount("cal", uiview.SlotPanel, uiview.Content{View: gridView(42, 3, "Event with a long name 会議 🎉"), Intents: []uiview.IntentDecl{gridPickIntent}})
				m := f.loop.drawn()[0]
				lines := m.Render(width)
				if limit := viewPanelSlotRows(rows); len(lines) > limit {
					t.Fatalf("%d rows in a slot of %d", len(lines), limit)
				}
				for i, line := range lines {
					if w := widthx.VisibleWidth(line); w != width {
						t.Fatalf("row %d is %d cells: %q", i, w, widthx.StripAnsi(line))
					}
				}
				if strings.Contains(strings.Join(lines, "\n"), viewCutRow) && rows == 100 {
					t.Fatal("a month in a tall terminal was cut")
				}
				t.Logf("%d columns, %d terminal rows: %d rows drawn", width, rows, len(lines))
			})
		}
	}
}

// pig additive (D614): the terminal declares the grid kind; a month and a week fit its limits; the largest legal grid
// (128 cells of 8 chips of 256 bytes) is refused by the existing snapshot byte limit with the existing code.
func TestViewClientGridAgainstTheTerminalsLimits(t *testing.T) {
	if !slices.Contains(terminalViewAbilities(viewSize{}).Nodes, "grid") {
		t.Fatal("the terminal does not declare the grid kind")
	}
	f := newViewFixture(t)
	for _, n := range []struct {
		key        string
		cells, per int
	}{{"month", 42, 3}, {"week", 7, 8}} {
		if _, err := f.host.reg.Mount("ext", uiview.MountArgs{Key: n.key, Slot: uiview.SlotPanel, View: gridView(n.cells, n.per, "event")}); err != nil {
			t.Fatalf("%s: %v", n.key, err)
		}
	}
	chip := strings.Repeat("c", 250)
	_, err := f.host.reg.Mount("ext", uiview.MountArgs{Key: "largest", Slot: uiview.SlotPanel, View: gridView(128, 8, chip)})
	if err == nil || err.Code != uiview.CodeLimit || !strings.Contains(err.Message, "snapshot bytes") {
		t.Fatalf("the largest grid (%d bytes) was answered %v, want code limit on snapshot bytes", len(gridView(128, 8, chip)), err)
	}
	t.Logf("the largest legal grid: %d bytes of view JSON; the terminal takes %d: %v", len(gridView(128, 8, chip)), terminalViewLimits.SnapshotBytes, err)
}

// pig additive (D614): in both modes a grid mounted through the bridge is accepted, drawn, and a grid one past a bound or
// with a member the record lacks is refused with code invalid and draws nothing.
func TestTerminalViewClientGridMountAndRefusal(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				p := newViewModeProbe(t, mode, true)
				good := `{"key":"cal","slot":"panel","view":` + string(gridView(14, 2, "standup")) + `}`
				if _, code := p.call("ui.view.mount", good); code != "" {
					t.Fatalf("a grid mount answered %q", code)
				}
				frame := widthx.StripAnsi(p.frame())
				for _, want := range []string{"Mon", "standup", "│"} {
					if !strings.Contains(frame, want) {
						t.Fatalf("the grid was not drawn (%q missing): %q", want, frame)
					}
				}
				for name, bad := range map[string]string{
					"columns":        `{"kind":"grid","id":"g","columns":17}`,
					"unknown member": `{"kind":"grid","id":"g","columns":2,"spans":[]}`,
					"chip member":    `{"kind":"grid","id":"g","columns":1,"cells":[{"chips":[{"text":"a","kind":"b"}]}]}`,
				} {
					if _, code := p.call("ui.view.mount", `{"key":"bad","slot":"panel","view":{"root":`+bad+`}}`); code != "invalid" {
						t.Errorf("%s: a bad grid mount answered %q, want invalid", name, code)
					}
				}
				if _, code := p.call("ui.view.mount", `{"key":"in-stack","slot":"panel","view":{"root":{"kind":"vstack","children":[{"kind":"grid","id":"g","columns":0}]}}}`); code != "invalid" {
					t.Errorf("a bad grid inside a stack answered %q, want invalid", code)
				}
			})
		})
	}
}
