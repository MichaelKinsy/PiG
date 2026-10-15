package tui

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// spacer.ts: lines empty strings; setLines changes the count. truncated-text.ts: padding rows above and below, the first line of the content truncated with "..." into the width minus the horizontal padding, every row padded to the width.
// Pi: packages/tui/src/components/spacer.ts, truncated-text.ts.
func TestSpacerAndTruncatedTextRender(t *testing.T) {
	spacer := NewSpacer(2)
	if got := spacer.Render(10); !reflect.DeepEqual(got, []string{"", ""}) {
		t.Fatalf("Spacer(2) = %q", got)
	}
	spacer.SetLines(3)
	if got := spacer.Render(10); len(got) != 3 {
		t.Fatalf("after SetLines(3) = %q", got)
	}

	text := NewTruncatedText("hello world\nsecond line", 1, 1)
	got := stripANSILines(text.Render(10))
	want := []string{"          ", " hello... ", "          "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TruncatedText = %q, want %q", got, want)
	}
	asymmetric := stripANSILines(NewTruncatedText("abcdefghij", 2, 0).Render(10))
	if !reflect.DeepEqual(asymmetric, []string{"  abc...  "}) {
		t.Fatalf("horizontal padding rows = %q", asymmetric)
	}
	if rows := NewTruncatedText("x", 0, 2).Render(4); len(rows) != 5 {
		t.Fatalf("vertical padding gives %d rows, want 5", len(rows))
	}
	if firstLine := stripANSILines(NewTruncatedText("hi\nthere", 0, 0).Render(12)); !reflect.DeepEqual(firstLine, []string{"hi          "}) {
		t.Fatalf("multi-line content = %q, want only its first line", firstLine)
	}
	if short := stripANSILines(NewTruncatedText("hi", 0, 0).Render(6)); !reflect.DeepEqual(short, []string{"hi    "}) {
		t.Fatalf("short content = %q, want it padded to the width", short)
	}
}

// stack.ts HStack and VStack: a horizontal stack puts its children side by side with the gap, a vertical stack puts them in rows.
// Pi: packages/tui/src/components/h-stack.ts, v-stack.ts.
func TestHStackAndVStackLayOutChildren(t *testing.T) {
	basis, noShrink := 6, 0
	two := func() []StackChild {
		options := StackEntryOptions{Basis: &basis, Shrink: &noShrink}
		return []StackChild{{Component: NewText("ab"), StackEntryOptions: options}, {Component: NewText("cd"), StackEntryOptions: options}}
	}
	cdColumn := func(gap int) int {
		h := NewHStack(two(), StackOptions{Gap: &gap})
		got := stripANSILines(h.Render(20))
		if len(got) != 1 || !reflect.DeepEqual(strings.Fields(got[0]), []string{"ab", "cd"}) {
			t.Fatalf("HStack = %q, want the children side by side on one row", got)
		}
		return strings.Index(got[0], "cd")
	}
	if narrow, wide := cdColumn(0), cdColumn(4); wide-narrow != 4 {
		t.Fatalf("second child at column %d with gap 4 and %d with gap 0, want the gap between them", wide, narrow)
	}
	v := NewVStack([]StackChild{{Component: NewText("ab")}, {Component: NewText("cd")}}, StackOptions{})
	if node, ok := NewHStack(two(), StackOptions{}).LayoutNode().(StackLayoutNode); !ok || node.Type != "hstack" {
		t.Fatalf("HStack layout node = %+v, want type hstack", node)
	}
	if node, ok := v.LayoutNode().(StackLayoutNode); !ok || node.Type != "vstack" {
		t.Fatalf("VStack layout node = %+v, want type vstack", node)
	}
	got := stripANSILines(v.Render(10))
	if len(got) != 2 || !strings.HasPrefix(got[0], "ab") || !strings.HasPrefix(got[1], "cd") {
		t.Fatalf("VStack = %q, want the children in rows", got)
	}
}

// tui-alt-screen.ts setCopyOnSelect changes automatic selection copy without rebuilding the renderer.
// Pi: packages/tui/src/tui-alt-screen.ts setCopyOnSelect.
func TestAltScreenSetCopyOnSelectTogglesAutomaticCopy(t *testing.T) {
	selectWord := func(renderer *TuiAltScreen, out *bytes.Buffer) bool {
		out.Reset()
		renderer.previousScreen = []string{"hello world"}
		renderer.HandleViewportInput("\x1b[<0;1;1M")
		renderer.HandleViewportInput("\x1b[<32;5;1M")
		renderer.HandleViewportInput("\x1b[<0;5;1m")
		return strings.Contains(out.String(), "\x1b]52;c;")
	}
	var out bytes.Buffer
	renderer := newAltScreenForTest(&out, 40, 6, TuiAltScreenOptions{})
	renderer.SetCopyOnSelect(false)
	if renderer.GetCopyOnSelect() || selectWord(renderer, &out) {
		t.Fatal("copy-on-select stayed on after SetCopyOnSelect(false)")
	}
	renderer.SetCopyOnSelect(true)
	if !renderer.GetCopyOnSelect() || !selectWord(renderer, &out) {
		t.Fatal("copy-on-select stayed off after SetCopyOnSelect(true)")
	}
}

// select-list.ts setSelectedIndex clamps the index into the filtered list from both ends.
// Pi: packages/tui/src/components/select-list.ts setSelectedIndex.
func TestSelectListSetSelectedIndexClampsBothEnds(t *testing.T) {
	list := NewSelectList([]SelectItem{{Value: "a", Label: "a"}, {Value: "b", Label: "b"}, {Value: "c", Label: "c"}}, 5, SelectListTheme{})
	for index, want := range map[int]string{-5: "a", 0: "a", 1: "b", 2: "c", 3: "c", 99: "c"} {
		list.SetSelectedIndex(index)
		if item, ok := list.SelectedItem(); !ok || item.Value != want {
			t.Errorf("SetSelectedIndex(%d) selects %q, want %q", index, item.Value, want)
		}
	}
}
