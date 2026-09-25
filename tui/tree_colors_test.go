package tui

import (
	"strings"
	"testing"
)

// TestStyleHelpersUseScopedResets locks the fg/dim helpers to upstream's
// scoped reset convention (theme.fg closes with SGR 39, chalk.dim with SGR
// 22). A full SGR 0 reset clears any surrounding background mid-line, which
// breaks the /tree selected-row highlight. Red before the scoped-reset fix.
func TestStyleHelpersUseScopedResets(t *testing.T) {
	if got, want := fg("\x1b[31m", "x"), "\x1b[31mx\x1b[39m"; got != want {
		t.Errorf("fg must close with scoped fg reset (SGR 39) to preserve background; got %q want %q", got, want)
	}
	if got, want := dim("x"), "\x1b[2mx\x1b[22m"; got != want {
		t.Errorf("dim must close with scoped intensity reset (SGR 22); got %q want %q", got, want)
	}
}

func TestTreeRenderLongRowTruncationPreservesANSI(t *testing.T) {
	SetTheme("dark")
	th := ActiveTheme()
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "u", label: th.Accent + "user: " + SGRFgReset + strings.Repeat("long ", 40)},
	}}
	ts := NewTreeSelect("", root)
	lines := ts.Render(32)

	var selected string
	for _, ln := range lines {
		if strings.Contains(stripANSI(ln), "user:") {
			selected = ln
			break
		}
	}
	if selected == "" {
		t.Fatal("long tree row not rendered")
	}
	if !strings.Contains(selected, th.Accent+"user: "+SGRFgReset) {
		t.Fatalf("truncated tree row should preserve role colour; row=%q", selected)
	}
	if strings.Contains(selected, SGRResetAll) {
		t.Fatalf("truncated selected row must not contain full SGR 0; it clears the selected background: %q", selected)
	}
	if !strings.Contains(selected, th.SelectedBg) {
		t.Fatalf("selected row should keep full-width highlight; row=%q", selected)
	}
}

func TestTreeRenderBranchLabelUsesWarningOutsideSelectedContent(t *testing.T) {
	SetTheme("dark")
	th := ActiveTheme()
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNodeWithBranchLabel{fakeNode: fakeNode{id: "u", label: th.Accent + "user: " + SGRFgReset + "hello"}, branchLabel: "milestone"},
	}}
	ts := NewTreeSelect("", root)
	lines := ts.Render(80)

	var selected string
	for _, ln := range lines {
		if strings.Contains(ln, "milestone") {
			selected = ln
			break
		}
	}
	if selected == "" {
		t.Fatal("labeled row not rendered")
	}
	wantLabel := th.Warning + "[milestone] " + SGRFgReset
	if !strings.Contains(selected, wantLabel) {
		t.Fatalf("branch label should be warning-coloured like upstream; row=%q want contains %q", selected, wantLabel)
	}
	if !strings.Contains(selected, th.Accent+"user: "+SGRFgReset) {
		t.Fatalf("entry content should keep its own role colour; row=%q", selected)
	}
	labelIdx := strings.Index(selected, wantLabel)
	boldIdx := strings.Index(selected, "\x1b[1m")
	if boldIdx == -1 {
		t.Fatalf("selected entry content should be bold; row=%q", selected)
	}
	if labelIdx > boldIdx {
		t.Fatalf("branch label should be outside selected-entry bold span; labelIdx=%d boldIdx=%d row=%q", labelIdx, boldIdx, selected)
	}
}

type fakeNodeWithBranchLabel struct {
	fakeNode
	branchLabel string
}

func (n *fakeNodeWithBranchLabel) NodeBranchLabel() string { return n.branchLabel }

// TestTreeRenderSelectedRowStyling pins the /tree overlay's visible styling to
// upstream: a Border-coloured frame, a muted "Type to search:" line, an
// accent path bullet, and a bold selected row whose SelectedBg highlight spans
// the full width (no full SGR 0 reset clears it mid-row). Red before the fix:
// the separator was faint, the bullet plain, the selected content unbolded,
// and interior `\x1b[0m` resets cut the highlight short.
func TestTreeRenderSelectedRowStyling(t *testing.T) {
	SetTheme("dark")
	th := ActiveTheme()
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "u", label: "user: hi"},
		&fakeNode{id: "a", label: "assistant: yo"},
	}}
	ts := NewTreeSelect("", root)
	lines := ts.Render(80)
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, "\x1b[2m\u2500") {
		t.Error("separator must not be faint; upstream draws the frame in the Border colour")
	}
	if !strings.Contains(joined, th.Border+"\u2500") {
		t.Error("separator should be Border-coloured")
	}
	if !strings.Contains(joined, th.Muted+"Type to search:") {
		t.Error(`"Type to search:" should be muted`)
	}
	if !strings.Contains(joined, th.Accent+"\u2022 ") {
		t.Error("path bullet should be accent-coloured")
	}

	// The selected row (index 0, carries the › cursor) must be highlighted and
	// bold, with no full SGR 0 reset that would clear the background mid-row.
	var selected string
	for _, ln := range lines {
		if strings.Contains(ln, "\u203a") {
			selected = ln
			break
		}
	}
	if selected == "" {
		t.Fatal("no selected row rendered (expected › cursor)")
	}
	if !strings.Contains(selected, th.SelectedBg) {
		t.Error("selected row should carry the SelectedBg highlight")
	}
	if !strings.Contains(selected, "\x1b[1m") {
		t.Error("selected row content should be bold (upstream theme.bold)")
	}
	if strings.Contains(selected, "\x1b[0m") {
		t.Error("selected row must not contain a full SGR 0 reset; it clears the highlight background mid-row")
	}
}
