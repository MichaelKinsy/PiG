package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestFilterableListInitiallyShowsAll(t *testing.T) {
	f := NewFilterableList("Pick", []string{"alpha", "beta", "gamma"})
	if len(f.filtered) != 3 {
		t.Errorf("expected 3 visible, got %d", len(f.filtered))
	}
}

func TestFilterableListFilterTokensAreANDed(t *testing.T) {
	f := NewFilterableList("", []string{
		"alpha foo",
		"alpha bar",
		"beta foo",
	})
	for _, r := range "al fo" {
		f.HandleInput(string(r))
	}
	if f.filter != "al fo" {
		t.Fatalf("filter=%q", f.filter)
	}
	// Both tokens must be present: "alpha foo" qualifies; "alpha bar"
	// (no "fo") and "beta foo" (no "al") are filtered out.
	if len(f.filtered) != 1 {
		t.Errorf("expected 1 match, got %d", len(f.filtered))
	}
}

func TestFilterableListBackspaceShrinksFilter(t *testing.T) {
	f := NewFilterableList("", []string{"alpha", "beta"})
	f.HandleInput("a")
	f.HandleInput("\x7f")
	if f.filter != "" {
		t.Errorf("filter=%q after backspace", f.filter)
	}
	if len(f.filtered) != 2 {
		t.Errorf("expected all back, got %d", len(f.filtered))
	}
}

func TestFilterableListEnterCommitsSelectedIndex(t *testing.T) {
	f := NewFilterableList("", []string{"alpha", "beta", "gamma"})
	f.HandleInput("\x1b[B") // down
	f.HandleInput("\r")
	if !f.Done() || f.Cancelled() {
		t.Fatalf("done=%v cancelled=%v", f.Done(), f.Cancelled())
	}
	if f.SelectedIndex() != 1 {
		t.Errorf("selected=%d want 1", f.SelectedIndex())
	}
}

func TestFilterableListEscCancels(t *testing.T) {
	f := NewFilterableList("", []string{"a", "b"})
	f.HandleInput("\x1b")
	if !f.Done() || !f.Cancelled() {
		t.Fatalf("done=%v cancelled=%v", f.Done(), f.Cancelled())
	}
	if f.SelectedIndex() != -1 {
		t.Errorf("expected -1 on cancel, got %d", f.SelectedIndex())
	}
}

func TestFilterableListUpWrapsToBottom(t *testing.T) {
	f := NewFilterableList("", []string{"alpha", "beta", "gamma"})
	f.HandleInput("\x1b[A")
	if f.cursor != 2 {
		t.Fatalf("cursor=%d want 2 after wrap-up", f.cursor)
	}
}

func TestFilterableListDownWrapsToTop(t *testing.T) {
	f := NewFilterableList("", []string{"alpha", "beta", "gamma"})
	f.SetCursor(2)
	f.HandleInput("\x1b[B")
	if f.cursor != 0 {
		t.Fatalf("cursor=%d want 0 after wrap-down", f.cursor)
	}
}

func TestFilterableListCursorClampsAfterFilter(t *testing.T) {
	f := NewFilterableList("", []string{"alpha", "beta", "gamma"})
	f.HandleInput("\x1b[B")
	f.HandleInput("\x1b[B") // cursor at index 2 (gamma)
	f.HandleInput("a")      // filter "a" → matches alpha, beta, gamma → all 3
	f.HandleInput("l")      // filter "al" → matches alpha only
	if len(f.filtered) != 1 {
		t.Fatalf("filtered=%d", len(f.filtered))
	}
	if f.cursor != 0 {
		t.Errorf("cursor=%d want 0 after filter shrunk", f.cursor)
	}
	f.HandleInput("\r")
	if f.SelectedIndex() != 0 {
		t.Errorf("selected idx=%d want 0 (alpha)", f.SelectedIndex())
	}
}

func TestFilterableListRendersBorderAndStatus(t *testing.T) {
	f := NewFilterableList("Sessions", []string{"one", "two"})
	rows := f.Render(40)
	if !strings.Contains(rows[0], "filter:") {
		t.Errorf("missing filter prompt in row 0: %q", rows[0])
	}
	// rows[1] is a blank spacer (upstream Spacer(1) after input)
	if rows[1] != "" {
		t.Errorf("expected blank spacer in row 1, got %q", rows[1])
	}
	// rows[2] should be the first session entry (selected, with accent cursor)
	if !strings.Contains(rows[2], "one") {
		t.Errorf("expected first item in row 2: %q", rows[2])
	}
	if !strings.Contains(rows[2], "→") {
		t.Errorf("expected upstream-style selected prefix in row 2: %q", rows[2])
	}
	if len(rows) != 4 {
		t.Errorf("expected no trailing hint row for short list, got %d rows: %v", len(rows), rows)
	}
}

func TestFilterableListNoMatchTextMatchesUpstream(t *testing.T) {
	f := NewFilterableList("", []string{"alpha", "beta"})
	for _, r := range "zzz" {
		f.HandleInput(string(r))
	}
	rows := f.Render(40)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "No matching commands") {
		t.Fatalf("missing upstream no-match text: %q", joined)
	}
}

func TestFilterableList_DisabledSearchIgnoresPrintableInput(t *testing.T) {
	f := NewFilterableList("Pick", []string{"alpha", "beta"})
	f.EnableSearch = false
	f.HandleInput("z")
	if got := f.FilterText(); got != "" {
		t.Fatalf("FilterText = %q, want empty when search disabled", got)
	}
	rows := f.Render(40)
	if len(rows) > 0 && strings.Contains(stripANSI(rows[0]), "filter:") {
		t.Fatalf("unexpected search input row in render: %q", rows[0])
	}
}

func TestFilterableList_DisabledSearchIgnoresNonSelectListNavigation(t *testing.T) {
	list := NewFilterableList("", []string{"one", "two", "three"})
	list.EnableSearch = false
	list.HandleInput("\x1b[6~")
	list.HandleInput("\x1b[F")
	if list.CursorIndex() != 0 {
		t.Fatalf("SelectList-compatible mode accepted PageDown/End: %d", list.CursorIndex())
	}
}

func TestFilterableListLongListRendersCompactScrollInfo(t *testing.T) {
	labels := make([]string, 30)
	for i := range labels {
		labels[i] = fmt.Sprintf("item-%02d", i)
	}
	f := NewFilterableList("", labels)
	f.SetCursor(25)
	rows := f.Render(40)
	last := stripANSI(rows[len(rows)-1])
	if last != "  (26/30)" {
		t.Fatalf("scroll info = %q, want %q", last, "  (26/30)")
	}
}

func visibleIndexOf(t *testing.T, line, text string) int {
	t.Helper()
	index := strings.Index(line, text)
	if index < 0 {
		t.Fatalf("line %q does not contain %q", line, text)
	}
	return widthx.VisibleWidth(line[:index])
}

// Ported from select-list.test.ts "normalizes multiline descriptions to single line".
func TestFilterableListNormalizesMultilineDescriptions(t *testing.T) {
	list := NewFilterableList("", []string{"test"})
	list.EnableSearch = false
	list.MaxVisible = 5
	list.Descriptions = []string{"Line one\nLine two\nLine three"}

	rendered := list.Render(100)
	if len(rendered) == 0 {
		t.Fatal("render returned no rows")
	}
	plain := stripANSI(rendered[0])
	if strings.Contains(plain, "\n") || !strings.Contains(plain, "Line one Line two Line three") {
		t.Fatalf("description was not normalized: %q", plain)
	}
}

// Ported from select-list.test.ts "keeps descriptions aligned when the primary text is truncated".
func TestFilterableListKeepsDescriptionsAlignedAfterPrimaryTruncation(t *testing.T) {
	list := NewFilterableList("", []string{"short", "very-long-command-name-that-needs-truncation"})
	list.EnableSearch = false
	list.MaxVisible = 5
	list.Descriptions = []string{"short description", "long description"}

	rendered := list.Render(80)
	if got, want := visibleIndexOf(t, rendered[0], "short description"), visibleIndexOf(t, rendered[1], "long description"); got != want {
		t.Fatalf("description columns = %d and %d", got, want)
	}
}

// Ported from select-list.test.ts "uses the configured minimum primary column width".
func TestFilterableListUsesConfiguredMinimumPrimaryColumnWidth(t *testing.T) {
	list := NewFilterableList("", []string{"a", "bb"})
	list.EnableSearch = false
	list.MaxVisible = 5
	list.Descriptions = []string{"first", "second"}
	list.MinPrimaryColumnWidth = 12
	list.MaxPrimaryColumnWidth = 20

	rendered := list.Render(80)
	if got := visibleIndexOf(t, rendered[0], "first"); got != 14 {
		t.Fatalf("first description column = %d, want 14", got)
	}
	if got := visibleIndexOf(t, rendered[1], "second"); got != 14 {
		t.Fatalf("second description column = %d, want 14", got)
	}
}

// Ported from select-list.test.ts "uses the configured maximum primary column width".
func TestFilterableListUsesConfiguredMaximumPrimaryColumnWidth(t *testing.T) {
	list := NewFilterableList("", []string{"very-long-command-name-that-needs-truncation", "short"})
	list.EnableSearch = false
	list.MaxVisible = 5
	list.Descriptions = []string{"first", "second"}
	list.MinPrimaryColumnWidth = 12
	list.MaxPrimaryColumnWidth = 20

	rendered := list.Render(80)
	if got := visibleIndexOf(t, rendered[0], "first"); got != 22 {
		t.Fatalf("first description column = %d, want 22", got)
	}
	if got := visibleIndexOf(t, rendered[1], "second"); got != 22 {
		t.Fatalf("second description column = %d, want 22", got)
	}
}

// Ported from select-list.test.ts "allows overriding primary truncation while preserving description alignment".
func TestFilterableListAllowsPrimaryTruncationOverride(t *testing.T) {
	list := NewFilterableList("", []string{"very-long-command-name-that-needs-truncation", "short"})
	list.EnableSearch = false
	list.MaxVisible = 5
	list.Descriptions = []string{"first", "second"}
	list.MinPrimaryColumnWidth = 12
	list.MaxPrimaryColumnWidth = 12
	list.TruncatePrimary = func(context SelectListTruncatePrimaryContext) string {
		if widthx.VisibleWidth(context.Text) <= context.MaxWidth {
			return context.Text
		}
		return widthx.TruncateToWidth(context.Text, max(0, context.MaxWidth-1), "", false) + "…"
	}

	rendered := list.Render(80)
	if !strings.Contains(rendered[0], "…") {
		t.Fatalf("custom truncation missing ellipsis: %q", rendered[0])
	}
	if got, want := visibleIndexOf(t, rendered[0], "first"), visibleIndexOf(t, rendered[1], "second"); got != want {
		t.Fatalf("description columns = %d and %d", got, want)
	}
}

// ─── TreeSelect ──────────────────────────────────────────────────────────────

type fakeNode struct {
	id    string
	label string
	kids  []TreeNode
}

func (n *fakeNode) NodeID() string           { return n.id }
func (n *fakeNode) NodeLabel() string        { return n.label }
func (n *fakeNode) NodeChildren() []TreeNode { return n.kids }

func TestTreeSelectFlattensDFS(t *testing.T) {
	root := &fakeNode{id: "root", kids: []TreeNode{
		&fakeNode{id: "a", label: "A", kids: []TreeNode{
			&fakeNode{id: "a1", label: "A1"},
			&fakeNode{id: "a2", label: "A2"},
		}},
		&fakeNode{id: "b", label: "B"},
	}}
	ts := NewTreeSelect("Pick", root)
	if len(ts.rows) != 4 {
		t.Fatalf("rows=%d want 4", len(ts.rows))
	}
	want := []string{"a", "a1", "a2", "b"}
	for i, r := range ts.rows {
		if r.id != want[i] {
			t.Errorf("rows[%d].id=%q want %q", i, r.id, want[i])
		}
	}
}

func TestTreeSelectEnterCommitsCurrentID(t *testing.T) {
	root := &fakeNode{id: "root", kids: []TreeNode{
		&fakeNode{id: "a", label: "A"},
		&fakeNode{id: "b", label: "B"},
	}}
	ts := NewTreeSelect("", root)
	ts.HandleInput("\x1b[B")
	ts.HandleInput("\r")
	if !ts.Done() {
		t.Fatal("not done")
	}
	if ts.SelectedID() != "b" {
		t.Errorf("selected=%q want b", ts.SelectedID())
	}
}

func TestTreeSelectEscCancels(t *testing.T) {
	ts := NewTreeSelect("", &fakeNode{id: "r", kids: []TreeNode{&fakeNode{id: "x"}}})
	ts.HandleInput("\x1b")
	if !ts.Done() || !ts.Cancelled() {
		t.Fatalf("done=%v cancelled=%v", ts.Done(), ts.Cancelled())
	}
	if ts.SelectedID() != "" {
		t.Errorf("selected=%q want empty on cancel", ts.SelectedID())
	}
}

func TestTreeSelectRenderShowsConnectors(t *testing.T) {
	// Single session root with two children → the branch point is
	// at the children, so they get ├─/└─ connectors. (At the virtual
	// root layer, kids of `r` are session roots and render flat per
	// upstream tree-selector.ts:632: isVirtualRootChild suppresses
	// the connector.)
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "a", label: "A"},
			&fakeNode{id: "b", label: "B"},
		}},
	}}
	ts := NewTreeSelect("", root)
	rows := ts.Render(40)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "├─") {
		t.Errorf("expected ├─ connector in: %q", joined)
	}
	if !strings.Contains(joined, "└─") {
		t.Errorf("expected └─ connector for last child in: %q", joined)
	}
}

func TestTreeSelectEmptyTree(t *testing.T) {
	ts := NewTreeSelect("", &fakeNode{id: "r"})
	if len(ts.rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(ts.rows))
	}
	rows := ts.Render(40)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "No entries found") {
		t.Errorf("missing empty-tree text: %q", joined)
	}
	if !strings.Contains(joined, "(0/0)") {
		t.Errorf("missing empty-tree count: %q", joined)
	}
}

// ─── OpenOverlay ─────────────────────────────────────────────────────────────

func TestOpenOverlayPushesAndActiveOverlayReturns(t *testing.T) {
	tu := New()
	if tu.ActiveOverlay() != nil {
		t.Fatal("expected no active overlay on fresh TUI")
	}
	f := NewFilterableList("test", []string{"x"})
	h := tu.OpenOverlay(f, OverlayOptions{Title: "Test"})
	if tu.ActiveOverlay() != f {
		t.Errorf("ActiveOverlay didn't return the pushed component")
	}
	h.Close()
	if tu.ActiveOverlay() != nil {
		t.Errorf("expected nil after Close()")
	}
}

// TestFilterableList_UserOverrideRemapsNavigation proves the FilterableList,
// ModelSelector, TreeSelect, and ConfigSelector all route through the global
// TUI keybinding registry: if any of them reintroduces hardcoded byte
// switches for tui.select.*, this test breaks for FilterableList; the
// equivalent regression markers for the others live in the codingagent
// session_selector tests and the model_select_test.go suite.
func TestFilterableList_UserOverrideRemapsNavigation(t *testing.T) {
	prev := GetTUIKeybindings()
	defer SetTUIKeybindings(prev)

	// User remaps tui.select.down off the arrow key onto ctrl+r.
	SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{
		"tui.select.down": {"ctrl+r"},
	}))

	f := NewFilterableList("pick", []string{"alpha", "beta", "gamma"})
	// Default cursor is 0. The down arrow should no longer move it
	// (user removed that binding); ctrl+r should now move it down.
	f.HandleInput("\x1b[B") // down arrow: should be a no-op for navigation now
	if f.cursor != 0 {
		// FilterableList default branch silently drops control bytes,
		// so the cursor must stay put.
		t.Errorf("down arrow after rebind: cursor=%d, want 0", f.cursor)
	}
	f.HandleInput("\x12") // ctrl+r: newly-bound to tui.select.down
	if f.cursor != 1 {
		t.Errorf("ctrl+r after rebind should move down; cursor=%d, want 1", f.cursor)
	}
}
