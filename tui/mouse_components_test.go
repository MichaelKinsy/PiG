package tui

// These cases port the component-specific behavior from upstream
// mouse-components.test.ts that is not covered by the base mouse API tests.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func componentMouseEvent(eventType TuiMouseEventType, x, y int) TuiMouseEvent {
	button := MouseButtonLeft
	if eventType == MouseMove || eventType == MouseWheel {
		button = MouseButtonNone
	}
	return TuiMouseEvent{
		Type: eventType, Button: button,
		X: x, Y: y, ScreenX: x, ScreenY: y,
		Width: 80, Height: 10,
		ClickCount: 1,
	}
}

func TestMouseSelectsAndActivatesFilterableListRows(t *testing.T) {
	list := NewFilterableList("", []string{"A", "B", "C", "D", "E"})
	list.EnableSearch = false
	list.MaxVisible = 3
	list.Render(40)

	if result := DispatchMouseEvent(list, componentMouseEvent(MousePress, 1, 2)); result == nil || !result.Handled {
		t.Fatal("list did not handle row press")
	}
	if got := list.filtered[list.cursor]; got != 2 {
		t.Fatalf("pressed item index = %d, want 2", got)
	}
	if result := DispatchMouseEvent(list, componentMouseEvent(MouseClick, 1, 2)); result == nil || !result.Handled {
		t.Fatal("list did not handle row click")
	}
	if got := list.SelectedIndex(); got != 2 {
		t.Fatalf("selected item index = %d, want 2", got)
	}
}

func TestMouseFilterableListOffsetsSearchRows(t *testing.T) {
	list := NewFilterableList("", []string{"A", "B", "C"})
	list.MaxVisible = 3
	list.Render(40)

	if result := DispatchMouseEvent(list, componentMouseEvent(MousePress, 1, 0)); result == nil || !result.Handled || !result.Focus {
		t.Fatalf("filter-row press = %+v, want handled focus", result)
	}
	if result := DispatchMouseEvent(list, componentMouseEvent(MouseClick, 1, 1)); result != nil {
		t.Fatalf("spacer-row click = %+v, want unhandled", result)
	}
	if result := DispatchMouseEvent(list, componentMouseEvent(MousePress, 1, 2)); result == nil || !result.Handled {
		t.Fatal("first rendered item press was not handled")
	}
	if result := DispatchMouseEvent(list, componentMouseEvent(MouseClick, 1, 2)); result == nil || !result.Handled {
		t.Fatal("first rendered item click was not handled")
	}
	if got := list.SelectedIndex(); got != 0 {
		t.Fatalf("first rendered item selected index = %d, want 0", got)
	}

	empty := NewFilterableList("", nil)
	empty.Render(40)
	if result := DispatchMouseEvent(empty, componentMouseEvent(MousePress, 1, 0)); result == nil || !result.Handled || !result.Focus {
		t.Fatalf("empty-list filter press = %+v, want handled focus", result)
	}
}

func TestMouseFilterableListWheelWithSearchRows(t *testing.T) {
	list := NewFilterableList("", []string{"A", "B", "C"})
	list.MaxVisible = 3
	list.Render(40)
	wheel := componentMouseEvent(MouseWheel, 1, 2)
	wheel.WheelDelta = 1

	if result := DispatchMouseEvent(list, wheel); result == nil || !result.Handled {
		t.Fatal("wheel over first rendered item was not handled")
	}
	if got := list.filtered[list.cursor]; got != 1 {
		t.Fatalf("wheel selection = %d, want 1", got)
	}
}

func TestMouseWheelMovesEditorAutocompleteSelection(t *testing.T) {
	editor := NewEditor()
	editor.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	editor.SetText("/")
	editor.Render(40)
	wheel := componentMouseEvent(MouseWheel, 1, editor.renderedVisibleLineCount+3)
	wheel.WheelDelta = 1

	result := DispatchMouseEvent(editor, wheel)
	if result == nil || !result.Handled || !result.Focus {
		t.Fatalf("wheel over autocomplete = %+v, want handled focus", result)
	}
	if got := editor.autocompleteCursor; got != 1 {
		t.Fatalf("autocomplete cursor = %d, want 1", got)
	}

	counterWheel := componentMouseEvent(MouseWheel, 1, editor.renderedVisibleLineCount+3+editor.renderedAutocompleteHeight-1)
	counterWheel.WheelDelta = 1
	result = DispatchMouseEvent(editor, counterWheel)
	if result == nil || !result.Handled || !result.Focus {
		t.Fatalf("wheel over autocomplete counter = %+v, want handled focus", result)
	}
	if got := editor.autocompleteCursor; got != 2 {
		t.Fatalf("autocomplete cursor after counter wheel = %d, want 2", got)
	}
}

func TestMouseActivatesSettingsRows(t *testing.T) {
	list := NewSettingsListWithOptions([]SettingItem{
		{ID: "mode", Label: "Mode", CurrentValue: "one", Values: []string{"one", "two"}},
		{ID: "other", Label: "Other", CurrentValue: "off", Values: []string{"off", "on"}},
		{ID: "third", Label: "Third", CurrentValue: "low", Values: []string{"low", "high"}},
		{ID: "fourth", Label: "Fourth", CurrentValue: "x", Values: []string{"x", "y"}},
	}, 3, false)
	list.Render(40)

	DispatchMouseEvent(list, componentMouseEvent(MousePress, 1, 2))
	DispatchMouseEvent(list, componentMouseEvent(MouseClick, 1, 2))
	if list.ChangedID != "third" || list.ChangedValue != "high" {
		t.Fatalf("settings change = %q/%q, want third/high", list.ChangedID, list.ChangedValue)
	}
}

func TestMouseIgnoresHoverAndClicksVisibleRowsAfterScrolling(t *testing.T) {
	for _, row := range []int{0, 4} {
		t.Run(fmt.Sprintf("select-row-%d", row), func(t *testing.T) {
			labels := make([]string, 12)
			for i := range labels {
				labels[i] = fmt.Sprintf("Item %d", i)
			}
			list := NewFilterableList("", labels)
			list.EnableSearch = false
			list.MaxVisible = 5
			list.SetCursor(5)
			wheel := componentMouseEvent(MouseWheel, 1, row)
			wheel.WheelDelta = 1
			DispatchMouseEvent(list, wheel)
			if got := list.filtered[list.cursor]; got != 6 {
				t.Fatalf("wheel selection = %d, want 6", got)
			}
			before := list.Render(80)
			if !regexp.MustCompile(fmt.Sprintf(`Item %d\s*$`, 4+row)).MatchString(stripANSI(before[row])) {
				t.Fatalf("visible row %d = %q", row, before[row])
			}
			for _, y := range []int{0, 1, 2, 3, 4, row} {
				if result := DispatchMouseEvent(list, componentMouseEvent(MouseMove, 1, y)); result != nil {
					t.Fatalf("hover row %d was handled: %+v", y, result)
				}
				if got := list.Render(80); !slices.Equal(got, before) {
					t.Fatalf("hover row %d changed render", y)
				}
			}
			DispatchMouseEvent(list, componentMouseEvent(MousePress, 1, row))
			list.Render(80)
			DispatchMouseEvent(list, componentMouseEvent(MouseClick, 1, row))
			if got := list.SelectedIndex(); got != 4+row {
				t.Fatalf("clicked item = %d, want %d", got, 4+row)
			}
		})

		t.Run(fmt.Sprintf("settings-row-%d", row), func(t *testing.T) {
			items := make([]SettingItem, 12)
			for i := range items {
				items[i] = SettingItem{
					ID: fmt.Sprintf("item-%d", i), Label: fmt.Sprintf("Item %d", i),
					Description:  fmt.Sprintf("Description %d", i),
					CurrentValue: "off", Values: []string{"off", "on"},
				}
			}
			list := NewSettingsListWithOptions(items, 5, true)
			list.cursor = 5
			wheel := componentMouseEvent(MouseWheel, 1, row+2)
			wheel.WheelDelta = 1
			DispatchMouseEvent(list, wheel)
			before := list.Render(80)
			if !strings.Contains(stripANSI(before[4]), "Item 6") || !strings.Contains(stripANSI(before[row+2]), fmt.Sprintf("Item %d", 4+row)) {
				t.Fatalf("unexpected scrolled settings rows: %q", before)
			}
			for _, y := range []int{0, 1, 2, 3, 4, row} {
				if result := DispatchMouseEvent(list, componentMouseEvent(MouseMove, 1, y+2)); result != nil {
					t.Fatalf("hover row %d was handled: %+v", y, result)
				}
				if got := list.Render(80); !slices.Equal(got, before) {
					t.Fatalf("hover row %d changed render", y)
				}
			}
			DispatchMouseEvent(list, componentMouseEvent(MousePress, 1, row+2))
			list.Render(80)
			DispatchMouseEvent(list, componentMouseEvent(MouseClick, 1, row+2))
			if list.ChangedID != fmt.Sprintf("item-%d", 4+row) || list.ChangedValue != "on" {
				t.Fatalf("settings change = %q/%q", list.ChangedID, list.ChangedValue)
			}
		})
	}
}

func TestMousePositionsAndFocusesEditorThroughAltScreenDispatch(t *testing.T) {
	h := newAltHarness(t, 20, 6, TuiAltScreenOptions{})
	editor := NewEditor()
	editor.SetText("hello")
	h.tui.Add(editor)
	h.start()

	h.send("\x1b[<0;3;3M", "\x1b[<0;3;3m", "X")
	if focused := h.tui.FocusedComponent(); focused != editor {
		t.Fatalf("focused component = %T, want editor", focused)
	}
	if got := editor.Text(); got != "heXllo" {
		t.Fatalf("editor text = %q, want heXllo", got)
	}
}

func TestMouseSelectsAndCopiesEditorTextOnDrag(t *testing.T) {
	var copied []string
	h := newAltHarness(t, 20, 6, TuiAltScreenOptions{
		CopySelection: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	editor := NewEditor()
	editor.SetText("hello world")
	h.tui.Add(editor)
	h.start()
	cursorBefore := editor.cursor

	h.send("\x1b[<0;1;3M", "\x1b[<32;5;3M", "\x1b[<0;5;3m")
	if !slices.Equal(copied, []string{"hello"}) {
		t.Fatalf("copied = %q, want [hello]", copied)
	}
	if editor.cursor != cursorBefore {
		t.Fatalf("editor cursor = %v, want %v", editor.cursor, cursorBefore)
	}
}
