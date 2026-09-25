package tui

// i unit tests for the upstream-mirrored flatten rules
// in TreeSelect. Each test asserts the precomputed `prefix` field
// of `treeRow` (no rendering) so that connector / gutter logic can
// be verified independently of width-aware Render output.
//
// Reference: upstream tree-selector.ts:143-260 (flattenTree),
// :631-666 (per-row prefix construction).

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// rowPrefixes returns the non-empty prefix slice for assertion. We
// also collect labels so failures show which row had the wrong
// prefix.
func rowPrefixes(t *TreeSelect) []string {
	out := make([]string, len(t.rows))
	for i, r := range t.rows {
		out[i] = t.rowLabel(r) + "|" + r.prefix
	}
	return out
}

// chain builds a 4-deep linear chain a→b→c→d under a virtual root.
func chain() TreeNode {
	return &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "a", label: "A", kids: []TreeNode{
			&fakeNode{id: "b", label: "B", kids: []TreeNode{
				&fakeNode{id: "c", label: "C", kids: []TreeNode{
					&fakeNode{id: "d", label: "D"},
				}},
			}},
		}},
	}}
}

func TestFlattenLinearChainStaysFlat(t *testing.T) {
	ts := NewTreeSelect("", chain())
	if got := len(ts.rows); got != 4 {
		t.Fatalf("rows=%d want 4: %v", got, rowPrefixes(ts))
	}
	for i, r := range ts.rows {
		if r.prefix != "" {
			t.Errorf("row[%d] %q: prefix=%q want empty (linear chain stays flat)", i, ts.rowLabel(r), r.prefix)
		}
		if strings.ContainsAny(r.prefix, "├└─│") {
			t.Errorf("row[%d] %q: prefix has tree glyphs %q (single-child chain should not draw connectors)", i, ts.rowLabel(r), r.prefix)
		}
	}
}

func TestFlattenSingleForkAtDepth1(t *testing.T) {
	// r → top (single root) → A, B (fork)
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "a", label: "A"},
			&fakeNode{id: "b", label: "B"},
		}},
	}}
	ts := NewTreeSelect("", root)
	if got := len(ts.rows); got != 3 {
		t.Fatalf("rows=%d want 3: %v", got, rowPrefixes(ts))
	}
	// TOP: single root, displayIndent=0, no connector.
	if ts.rows[0].prefix != "" {
		t.Errorf("TOP row prefix=%q want empty (single root, flat)", ts.rows[0].prefix)
	}
	// A: branch child, displayIndent=1, ├─ at column 0.
	if ts.rows[1].prefix != "├─ " {
		t.Errorf("A row prefix=%q want %q", ts.rows[1].prefix, "├─ ")
	}
	// B: last branch child, displayIndent=1, └─ at column 0.
	if ts.rows[2].prefix != "└─ " {
		t.Errorf("B row prefix=%q want %q", ts.rows[2].prefix, "└─ ")
	}
}

func TestFlattenChainBranchChain(t *testing.T) {
	// r → top → mid (single chain so far) → A, B (fork) ; A has a single child A1.
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "mid", label: "MID", kids: []TreeNode{
				&fakeNode{id: "a", label: "A", kids: []TreeNode{
					&fakeNode{id: "a1", label: "A1"},
				}},
				&fakeNode{id: "b", label: "B"},
			}},
		}},
	}}
	ts := NewTreeSelect("", root)
	if got := len(ts.rows); got != 5 {
		t.Fatalf("rows=%d want 5: %v", got, rowPrefixes(ts))
	}
	// Top + MID: linear chain, indent stays 0, no connectors.
	if ts.rows[0].prefix != "" || ts.rows[1].prefix != "" {
		t.Errorf("TOP/MID prefixes %q/%q want empty (linear chain segment)", ts.rows[0].prefix, ts.rows[1].prefix)
	}
	// A: branch child (not last), displayIndent=1, ├─.
	if ts.rows[2].prefix != "├─ " {
		t.Errorf("A row prefix=%q want %q", ts.rows[2].prefix, "├─ ")
	}
	// A1: justBranched && indent>0 → +1 indent (visual grouping under the branch);
	// showConnector=false (single child of A). MID's connector was suppressed
	// (linear chain), so no gutters yet: but A's connector WAS drawn (├─, !isLast),
	// so a gutter at position (1-1)=0 is carried down: "│  " then 3 blanks.
	if ts.rows[3].prefix != "│     " {
		t.Errorf("A1 row prefix=%q want %q (gutter from A + flat at indent 2)", ts.rows[3].prefix, "│     ")
	}
	// B: last branch child, displayIndent=1, └─.
	if ts.rows[4].prefix != "└─ " {
		t.Errorf("B row prefix=%q want %q", ts.rows[4].prefix, "└─ ")
	}
}

func TestFlattenMultipleRootsRenderFlat(t *testing.T) {
	// Upstream renders virtual-root children at displayIndent=0 without a connector.
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "r1", label: "R1"},
		&fakeNode{id: "r2", label: "R2"},
	}}
	ts := NewTreeSelect("", root)
	if got := len(ts.rows); got != 2 {
		t.Fatalf("rows=%d want 2: %v", got, rowPrefixes(ts))
	}
	for i, r := range ts.rows {
		if r.prefix != "" {
			t.Errorf("row[%d] %q: prefix=%q want empty (virtual-root children flatten)", i, ts.rowLabel(r), r.prefix)
		}
	}
}

func TestFlattenForkUnderForkGuttersStack(t *testing.T) {
	// r → top → A (with child A1+A2 inner fork), B
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "a", label: "A", kids: []TreeNode{
				&fakeNode{id: "a1", label: "A1"},
				&fakeNode{id: "a2", label: "A2"},
			}},
			&fakeNode{id: "b", label: "B"},
		}},
	}}
	ts := NewTreeSelect("", root)
	// TOP, A, A1, A2, B = 5 rows.
	if got := len(ts.rows); got != 5 {
		t.Fatalf("rows=%d want 5: %v", got, rowPrefixes(ts))
	}
	// A: indent=1, ├─, !isLast → gutter at col 0 carries to descendants.
	if ts.rows[1].prefix != "├─ " {
		t.Errorf("A prefix=%q want %q", ts.rows[1].prefix, "├─ ")
	}
	// A1: indent=2 (branch under A), gutter from A at col 0 (show=true),
	// connector ├─ at col 1.
	if ts.rows[2].prefix != "│  ├─ " {
		t.Errorf("A1 prefix=%q want %q", ts.rows[2].prefix, "│  ├─ ")
	}
	// A2: same gutter as A1, last sibling under A → └─.
	if ts.rows[3].prefix != "│  └─ " {
		t.Errorf("A2 prefix=%q want %q", ts.rows[3].prefix, "│  └─ ")
	}
	// B: last under TOP → └─, no carry-down gutters from above.
	if ts.rows[4].prefix != "└─ " {
		t.Errorf("B prefix=%q want %q", ts.rows[4].prefix, "└─ ")
	}
}

func TestTreePlainSideArrowsPageByVisibleWindow(t *testing.T) {
	kids := make([]TreeNode, 50)
	for i := range kids {
		kids[i] = &fakeNode{id: fmt.Sprintf("n%d", i), label: fmt.Sprintf("node-%d", i)}
	}
	ts := NewTreeSelect("", &fakeNode{id: "root", kids: kids})
	ts.cursor = 25
	ts.fixScroll()

	ts.HandleInput("\x1b[D") // plain left arrow, upstream: page up
	if ts.cursor != 5 {
		t.Fatalf("left arrow cursor=%d, want 5", ts.cursor)
	}
	ts.HandleInput("\x1b[C") // plain right arrow, upstream: page down
	if ts.cursor != 25 {
		t.Fatalf("right arrow cursor=%d, want 25", ts.cursor)
	}

	ts.HandleInput("\x1b[D")
	ts.HandleInput("\x1b[D")
	if ts.cursor != 0 {
		t.Fatalf("left arrow must clamp at start; cursor=%d", ts.cursor)
	}
	ts.cursor = len(ts.rows) - 5
	ts.HandleInput("\x1b[C")
	if ts.cursor != len(ts.rows)-1 {
		t.Fatalf("right arrow must clamp at end; cursor=%d want %d", ts.cursor, len(ts.rows)-1)
	}
}

func TestTreeHorizontalViewportKeepsDeepSelectedLabelVisible(t *testing.T) {
	leaf := &fakeNode{id: "n15", label: "deep-selected"}
	for i := 14; i >= 0; i-- {
		leaf = &fakeNode{id: fmt.Sprintf("n%d", i), label: fmt.Sprintf("node-%d", i), kids: []TreeNode{
			leaf,
			&fakeNode{id: fmt.Sprintf("s%d", i), label: fmt.Sprintf("sibling-%d", i)},
		}}
	}
	root := &fakeNode{id: "root", kids: []TreeNode{leaf}}
	ts := NewTreeSelect("", root)
	selected := -1
	for i, r := range ts.rows {
		if r.id == "n15" {
			selected = i
			break
		}
	}
	if selected < 0 {
		t.Fatal("deep selected row not found")
	}
	ts.cursor = selected
	ts.scroll = max(0, selected-5)

	lines := ts.Render(40)
	var selectedLine string
	for _, line := range lines {
		plain := stripANSI(line)
		if strings.Contains(plain, "›") {
			selectedLine = plain
			break
		}
	}
	if selectedLine == "" {
		t.Fatalf("selected row not rendered:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(selectedLine, "deep-selected") {
		t.Fatalf("selected deep label not visible after horizontal viewport; line=%q", selectedLine)
	}
}

// fakeNodeTS is a fakeNode that also implements
// TreeNodeWithLabelTimestamp so the picker exercises the timestamp
// rendering path for 3.1d-h.
type fakeNodeTS struct {
	fakeNode
	ts string
}

func (n *fakeNodeTS) NodeLabelTimestamp() string { return n.ts }

// ─── 3.1d-h: fold/unfold ─────────────────────────────────────────────

// TestFoldHidesDescendants: folding a branch-point node hides its
// subtree from the visible row list. Mirrors upstream
// tree-selector.ts:343-352 (foldedNodes filter pass).
func TestFoldHidesDescendants(t *testing.T) {
	useTreeKeybindings(t, nil)
	// r → top → A (2 children A1, A2), B
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "a", label: "A", kids: []TreeNode{
				&fakeNode{id: "a1", label: "A1"},
				&fakeNode{id: "a2", label: "A2"},
			}},
			&fakeNode{id: "b", label: "B"},
		}},
	}}
	ts := NewTreeSelect("", root)
	if got := len(ts.rows); got != 5 {
		t.Fatalf("pre-fold rows=%d want 5", got)
	}
	// Cursor on A. Drive Ctrl+← → fold A (it's foldable: nChildren=2,
	// parent TOP had multipleChildren=true).
	ts.cursor = 1 // A
	ts.HandleInput("\x1b[1;5D")
	// Post-fold visible: TOP(0), A(1, folded marker), B(2). 3 rows.
	// (A1, A2 are hidden as descendants of folded A.)
	wantIDs := []string{"top", "a", "b"}
	if len(ts.rows) != len(wantIDs) {
		t.Fatalf("post-fold rows=%d want %d (%v); got %v", len(ts.rows), len(wantIDs), wantIDs, rowPrefixes(ts))
	}
	for i, want := range wantIDs {
		if ts.rows[i].id != want {
			t.Errorf("row[%d]=%s want %s", i, ts.rows[i].id, want)
		}
	}
	// Unfold via Ctrl+→ on A (cursor still at idx 1 since visible
	// length didn't change row order).
	ts.cursor = 1
	ts.HandleInput("\x1b[1;5C")
	if got := len(ts.rows); got != 5 {
		t.Errorf("post-unfold rows=%d want 5", got)
	}
}

// TestFoldOnNonFoldableJumpsToSegmentStart: pressing Ctrl+← on a
// non-foldable interior row of a linear chain jumps the cursor to
// the chain's root (= segment start, since no multi-child ancestor
// exists above). Mirrors upstream tree-selector.ts:1042-1059: the
// up-walk returns the topmost current node when no branch point is
// found. Pre-3.1d-h.1 this was a one-row move; 3.1d-h.1 wired the
// real findBranchSegmentStart in.
func TestFoldOnNonFoldableJumpsToSegmentStart(t *testing.T) {
	useTreeKeybindings(t, nil)
	ts := NewTreeSelect("", chain()) // 4-deep linear chain a→b→c→d
	ts.cursor = 2                    // C: interior, non-foldable, no branch above
	ts.HandleInput("\x1b[1;5D")
	if ts.cursor != 0 {
		t.Errorf("Ctrl+← on linear-chain interior: cursor=%d want 0 (chain root)", ts.cursor)
	}
	if len(ts.foldedNodes) != 0 {
		t.Errorf("nothing should have been folded: foldedNodes=%v", ts.foldedNodes)
	}
}

// TestFoldRendersFoldedConnectorMarker: when a foldable row WITH a
// connector is folded, the connector's middle `─` is replaced by `⊞`.
// When foldable+expanded, by `⊟`. Mirrors upstream
// tree-selector.ts:655-661.
func TestFoldRendersFoldedConnectorMarker(t *testing.T) {
	useTreeKeybindings(t, nil)
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "a", label: "A", kids: []TreeNode{
				&fakeNode{id: "a1", label: "A1"},
			}},
			&fakeNode{id: "b", label: "B"},
		}},
	}}
	ts := NewTreeSelect("", root)
	// A is at idx 1 with prefix "├─ " (branch under TOP, !isLast).
	// A is foldable: nChildren=1 AND parent TOP has multipleChildren.
	rendered := ts.Render(80)
	// Pre-fold: A is foldable+expanded → connector should be ├⊟ and the
	// upstream-style active-path marker `• ` sits before the label text.
	// Strip ANSI since prefix is dim-colored to match upstream.
	if !strings.Contains(stripANSI(strings.Join(rendered, "\n")), "├⊟ • A") {
		t.Errorf("expected `├⊟ • A` (foldable+expanded) in pre-fold render:\n%s", strings.Join(rendered, "\n"))
	}
	ts.cursor = 1 // A
	ts.HandleInput("\x1b[1;5D")
	rendered = ts.Render(80)
	// Post-fold: A is folded → connector should be ├⊞ with the active-path bullet.
	if !strings.Contains(stripANSI(strings.Join(rendered, "\n")), "├⊞ • A") {
		t.Errorf("expected `├⊞ • A` (folded) in post-fold render:\n%s", strings.Join(rendered, "\n"))
	}
}

// TestLinearChainOnlyRootIsFoldable: in a 4-deep linear chain a→b→c→d,
// only `a` (the root) is foldable per upstream's isFoldable rule
// (root with children is always foldable; interior single-child
// nodes are not, because their parent has exactly 1 child). Mirrors
// tree-selector.ts:1010-1018.
func TestLinearChainOnlyRootIsFoldable(t *testing.T) {
	useTreeKeybindings(t, nil)
	ts := NewTreeSelect("", chain())
	if got := len(ts.rows); got != 4 {
		t.Fatalf("chain rows=%d want 4", got)
	}
	wantFoldable := []bool{true, false, false, false}
	for i, want := range wantFoldable {
		got := ts.isFoldable(ts.rows[i])
		if got != want {
			t.Errorf("row[%d] %q: isFoldable=%v want %v", i, ts.rowLabel(ts.rows[i]), got, want)
		}
	}
	// Folding the root should collapse to just the root itself.
	ts.cursor = 0
	ts.HandleInput("\x1b[1;5D")
	if len(ts.rows) != 1 {
		t.Errorf("post-fold-root visible rows=%d want 1 (only A); got %v", len(ts.rows), rowPrefixes(ts))
	}
}

// ─── 3.1d-h: label-timestamp toggle ──────────────────────────────────

// TestLabelTimestampHiddenByDefault: label-timestamp display is OFF
// initially. Pressing `T` toggles it on; the picker then renders
// the LabelEntry's hh:mm next to the row content.
func TestLabelTimestampToggleRendersTime(t *testing.T) {
	useTreeKeybindings(t, nil)
	// Build a single-root tree with one labeled node carrying a
	// fresh ISO timestamp (today, so the format collapses to hh:mm).
	now := time.Now().Local()
	tsStr := now.Format(time.RFC3339)
	wantHHMM := fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute())

	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNodeTS{fakeNode: fakeNode{id: "x", label: "[important] some-content"}, ts: tsStr},
	}}
	ts := NewTreeSelect("", root)

	// Initially OFF: no timestamp in render.
	rendered := strings.Join(ts.Render(80), "\n")
	if strings.Contains(rendered, wantHHMM) {
		t.Errorf("timestamp leaked into default render (toggle should be OFF):\n%s", rendered)
	}
	// Toggle ON.
	ts.HandleInput("T")
	if !ts.showLabelTimestamps {
		t.Fatal("T should toggle showLabelTimestamps on")
	}
	rendered = strings.Join(ts.Render(80), "\n")
	if !strings.Contains(rendered, wantHHMM) {
		t.Errorf("expected timestamp %q in post-toggle render:\n%s", wantHHMM, rendered)
	}
	// Toggle OFF again.
	ts.HandleInput("T")
	if ts.showLabelTimestamps {
		t.Fatal("T should toggle showLabelTimestamps off")
	}
}

// TestFormatLabelTimestampForms covers the three upstream branches
// in tree-selector.ts:787-810: same-day, same-year, other-year.
func TestFormatLabelTimestampForms(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-05-01T15:30:00Z")
	now = now.Local()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"same-day", "2026-05-01T08:15:00Z", ""}, // computed below
		{"same-year-different-day", "2026-03-12T09:45:00Z", ""},
		{"different-year", "2024-12-31T23:59:00Z", ""},
		{"empty", "", ""},
		{"unparseable", "not-a-time", ""},
	}
	// Build expected dynamically using the same Local() conversion
	// formatLabelTimestamp does.
	expectFor := func(in string) string {
		if in == "" || in == "not-a-time" {
			return ""
		}
		tt, _ := time.Parse(time.RFC3339, in)
		tt = tt.Local()
		hhmm := fmt.Sprintf("%02d:%02d", tt.Hour(), tt.Minute())
		switch {
		case tt.Year() == now.Year() && tt.YearDay() == now.YearDay():
			return hhmm
		case tt.Year() == now.Year():
			return fmt.Sprintf("%d/%d %s", int(tt.Month()), tt.Day(), hhmm)
		default:
			return fmt.Sprintf("%02d/%d/%d %s", tt.Year()%100, int(tt.Month()), tt.Day(), hhmm)
		}
	}
	for i := range cases {
		cases[i].want = expectFor(cases[i].in)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatLabelTimestamp(tc.in, now)
			if got != tc.want {
				t.Errorf("formatLabelTimestamp(%q) = %q want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─── 3.1d-h.1: branch-segment-start navigation jump ──────────────────

// TestFindBranchSegmentStartUpJumpsToSegmentStart: with a tree
// containing a fork, pressing Ctrl+← from a non-foldable interior
// row jumps the cursor to the start of the current branch segment
// (mirrors upstream tree-selector.ts:1042-1059), not just one row up.
func TestFindBranchSegmentStartUpJumpsToSegmentStart(t *testing.T) {
	useTreeKeybindings(t, nil)
	// r → top → A (with single-child chain A1 → A2), B
	// Visible rows: TOP(0), A(1), A1(2), A2(3), B(4).
	// A is a branch child (parent TOP has 2 kids); A1, A2 are
	// non-foldable interior single-child rows.
	// Cursor on A2 → up should jump to A (segment start), not A1.
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "a", label: "A", kids: []TreeNode{
				&fakeNode{id: "a1", label: "A1", kids: []TreeNode{
					&fakeNode{id: "a2", label: "A2"},
				}},
			}},
			&fakeNode{id: "b", label: "B"},
		}},
	}}
	ts := NewTreeSelect("", root)
	if got := len(ts.rows); got != 5 {
		t.Fatalf("rows=%d want 5: %v", got, rowPrefixes(ts))
	}
	// IDs by index: top=0, a=1, a1=2, a2=3, b=4.
	ts.cursor = 3 // A2: non-foldable interior
	ts.HandleInput("\x1b[1;5D")
	if ts.cursor != 1 {
		t.Errorf("Ctrl+← from A2 (interior of A's segment): cursor=%d want 1 (A: segment start), rows=%v", ts.cursor, rowPrefixes(ts))
	}
}

// TestFindBranchSegmentStartUpFromSegmentStartClimbs: when cursor
// is already AT a segment start (e.g. cursor on A, the first child
// of TOP's branch), Ctrl+← should climb past it to TOP's segment
// (or to the top of the tree). Mirrors upstream's
// `segmentStart < selectedIndex` guard at :1051: when we're
// already at the start, keep going.
func TestFindBranchSegmentStartUpFromSegmentStartClimbs(t *testing.T) {
	useTreeKeybindings(t, nil)
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "a", label: "A"},
			&fakeNode{id: "b", label: "B"},
		}},
	}}
	ts := NewTreeSelect("", root)
	// Rows: TOP(0), A(1), B(2). Cursor on A: A IS a segment start
	// (first child of TOP's branch). Up should climb past A to TOP
	// (the visible-tree root, since there's no multi-child ancestor
	// above it).
	ts.cursor = 1
	ts.HandleInput("\x1b[1;5D")
	if ts.cursor != 0 {
		t.Errorf("Ctrl+← from segment-start A: cursor=%d want 0 (TOP), rows=%v", ts.cursor, rowPrefixes(ts))
	}
}

// TestFindBranchSegmentStartDownJumpsToFirstChildOfBranch:
// Down direction. From a non-folded row in a single-child chain,
// walk down the chain until we hit a multi-child node, then return
// its FIRST child's index. Mirrors upstream :1031-1041.
func TestFindBranchSegmentStartDownJumpsToFirstChildOfBranch(t *testing.T) {
	useTreeKeybindings(t, nil)
	// r → top → mid → A, B (fork at MID)
	// Rows: TOP(0), MID(1), A(2), B(3).
	// Cursor on TOP → walk down: TOP has 1 child MID; MID has 2 children;
	// return MID's first child A → index 2.
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "mid", label: "MID", kids: []TreeNode{
				&fakeNode{id: "a", label: "A"},
				&fakeNode{id: "b", label: "B"},
			}},
		}},
	}}
	ts := NewTreeSelect("", root)
	ts.cursor = 0 // TOP
	ts.HandleInput("\x1b[1;5C")
	if ts.cursor != 2 {
		t.Errorf("Ctrl+→ from TOP: cursor=%d want 2 (A: first child of MID's branch), rows=%v", ts.cursor, rowPrefixes(ts))
	}
}

// TestFindBranchSegmentStartLinearChainWalksToRootAndLeaf: on a
// pure linear chain (no multi-child branches anywhere), Ctrl+←
// from any non-foldable interior row jumps to the root, and Ctrl+→
// from any interior row jumps to the leaf. Mirrors upstream
// tree-selector.ts:1031-1041 (down) + :1056 (up returns root when
// no branch found). The earlier 3.1d-h "one-row fallback" is gone
// in 3.1d-h.1: the upstream behaviour IS the navigation aid.
func TestFindBranchSegmentStartLinearChainWalksToRootAndLeaf(t *testing.T) {
	useTreeKeybindings(t, nil)
	ts := NewTreeSelect("", chain()) // 4-deep linear chain a→b→c→d, rows[0..3]
	ts.cursor = 2                    // C: interior, non-foldable
	ts.HandleInput("\x1b[1;5D")
	if ts.cursor != 0 {
		t.Errorf("linear-chain Ctrl+← from interior: cursor=%d want 0 (root)", ts.cursor)
	}
	ts.cursor = 1 // B: interior
	ts.HandleInput("\x1b[1;5C")
	if ts.cursor != 3 {
		t.Errorf("linear-chain Ctrl+→ from interior: cursor=%d want 3 (leaf)", ts.cursor)
	}
}

// ─── 3.1d-g.1: filter modes + Tab cycling ──────────────────

// fakeNodeTagged implements TreeNode + TreeNodeWithFilterTags so
// the filter-mode tests can build trees with `settings`-tagged rows
// without dragging in the real session adapter. Mirrors the
// minimal contract pig's adapter populates.
type fakeNodeTagged struct {
	id    string
	label string
	tags  []string
	kids  []TreeNode
}

func (n *fakeNodeTagged) NodeID() string           { return n.id }
func (n *fakeNodeTagged) NodeLabel() string        { return n.label }
func (n *fakeNodeTagged) NodeChildren() []TreeNode { return n.kids }
func (n *fakeNodeTagged) NodeFilterTags() []string { return n.tags }

// TestFilterDefaultHidesSettingsTaggedRows: in `default` mode (the
// initial state), rows tagged `settings` are dropped from the
// visible list. Mirrors upstream tree-selector.ts:303-308.
func TestFilterDefaultHidesSettingsTaggedRows(t *testing.T) {
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user msg"},
		&fakeNodeTagged{id: "m", label: "model_change", tags: []string{"settings"}},
		&fakeNodeTagged{id: "a", label: "assistant"},
	}}
	ts := NewTreeSelect("", root)
	if ts.filterMode != "default" {
		t.Fatalf("filterMode=%q want default", ts.filterMode)
	}
	if len(ts.rows) != 2 {
		t.Errorf("default mode visible rows=%d want 2 (settings row hidden); rows=%v", len(ts.rows), rowPrefixes(ts))
	}
	for _, r := range ts.rows {
		if r.id == "m" {
			t.Errorf("settings row `m` should be hidden in default mode but appears: rows=%v", rowPrefixes(ts))
		}
	}
}

// TestFilterAllShowsEverything: cycling to `all` shows all rows
// including the settings-tagged ones. With 5 modes the cycle is
// default → no-tools → user-only → labeled-only → all.
func TestFilterAllShowsEverything(t *testing.T) {
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user msg"},
		&fakeNodeTagged{id: "m", label: "model_change", tags: []string{"settings"}},
		&fakeNodeTagged{id: "a", label: "assistant"},
	}}
	ts := NewTreeSelect("", root)
	// Cycle forward until we hit "all" (4 Tabs: default→no-tools→user-only→labeled-only→all).
	for range 4 {
		ts.HandleInput("\t")
	}
	if ts.filterMode != "all" {
		t.Fatalf("after 4 Tabs: filterMode=%q want all", ts.filterMode)
	}
	if len(ts.rows) != 3 {
		t.Errorf("all mode rows=%d want 3 (everything visible); rows=%v", len(ts.rows), rowPrefixes(ts))
	}
}

// TestFilterTabCyclesAndShiftTabReverses: Tab advances forward, Shift+Tab
// (\\x1b[Z) reverses. Tests the first two steps of the 5-mode cycle.
func TestFilterTabCyclesAndShiftTabReverses(t *testing.T) {
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user"},
	}}
	ts := NewTreeSelect("", root)
	if ts.filterMode != "default" {
		t.Fatalf("initial filterMode=%q want default", ts.filterMode)
	}
	ts.HandleInput("\t") // default → no-tools
	if ts.filterMode != "no-tools" {
		t.Errorf("Tab from default: filterMode=%q want no-tools", ts.filterMode)
	}
	ts.HandleInput("\x1b[Z") // Shift+Tab: no-tools → default
	if ts.filterMode != "default" {
		t.Errorf("Shift+Tab from no-tools: filterMode=%q want default", ts.filterMode)
	}
	// Wrap: from default, Shift+Tab should go to the last mode (all).
	ts.HandleInput("\x1b[Z") // default → all (wrap)
	if ts.filterMode != "all" {
		t.Errorf("Shift+Tab from default (wrap): filterMode=%q want all", ts.filterMode)
	}
}

// TestFilterCyclePreservesCursorWhenRowStillVisible: when cycling
// to a mode that still has the current row visible, the cursor
// stays put on the same id.
func TestFilterCyclePreservesCursorWhenRowStillVisible(t *testing.T) {
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user"},
		&fakeNodeTagged{id: "m", label: "settings", tags: []string{"settings"}},
		&fakeNodeTagged{id: "a", label: "assistant"},
	}}
	ts := NewTreeSelect("", root)
	// Move cursor onto `a` (the assistant row, idx=1 in default
	// mode because `m` is hidden).
	ts.cursor = 1
	curID := ts.rows[1].id
	if curID != "a" {
		t.Fatalf("setup: expected cursor 1 on `a`, got %q (rows=%v)", curID, rowPrefixes(ts))
	}
	ts.HandleInput("\t") // → all mode
	// In `all` mode `a` is at index 2 (after u, m). Cursor should
	// have followed.
	if ts.rows[ts.cursor].id != "a" {
		t.Errorf("after Tab: cursor on %q want `a` (cursor=%d, rows=%v)", ts.rows[ts.cursor].id, ts.cursor, rowPrefixes(ts))
	}
}

// treeHelpTestKeybindings installs upstream's default tree app bindings
// (darwin order) for the tree help tests and restores the previous manager.
func treeHelpTestKeybindings(t *testing.T, user map[string][]string) {
	t.Helper()
	prev := GetTUIKeybindings()
	t.Cleanup(func() { SetKeybindings(prev) })
	defs := TUIKeybindingDefinitionsFor(KeybindingPlatformFor("darwin", func(string) string { return "" }))
	for id, keys := range map[string][]string{
		"app.tree.foldOrUp":             {"alt+left", "ctrl+left"},
		"app.tree.unfoldOrDown":         {"alt+right", "ctrl+right"},
		"app.message.copy":              {"ctrl+x"},
		"app.tree.editLabel":            {"shift+l"},
		"app.tree.toggleLabelTimestamp": {"shift+t"},
		"app.tree.filter.default":       {"ctrl+d"},
		"app.tree.filter.noTools":       {"ctrl+t"},
		"app.tree.filter.userOnly":      {"ctrl+u"},
		"app.tree.filter.labeledOnly":   {"ctrl+l"},
		"app.tree.filter.all":           {"ctrl+a"},
		"app.tree.filter.cycleForward":  {"ctrl+o"},
		"app.tree.filter.cycleBackward": {"shift+ctrl+o"},
	} {
		defs[id] = TUIKeybindingDef{DefaultKeys: keys}
	}
	SetKeybindings(NewKeybindingsManager(defs, user))
}

// Pi 0.87.1 TreeHelp packs the key help into " · "-joined rows
// (selectors/09 probe at width 100, macOS key names).
func TestFilterRenderHeaderMatchesUpstreamShape(t *testing.T) {
	treeHelpTestKeybindings(t, nil)
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user"},
	}}
	ts := NewTreeSelect("", root)
	header := func() []string {
		var out []string
		for _, line := range ts.Render(100)[:5] {
			out = append(out, strings.TrimRight(stripANSI(line), " "))
		}
		return out
	}
	want := []string{
		strings.Repeat("─", 100),
		"   Session Tree",
		"  ↑/↓ move · ←/→ page · " + FormatKeyText("alt", false) + "+←/→ branch · ctrl+x copy · shift+l label · shift+t label time",
		"  filters ctrl+d/t/u/l/a · cycle ctrl+o/shift+ctrl+o",
		"  Type to search:",
	}
	if got := header(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("header =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Cycling filters keeps the header shape.
	for range 4 {
		ts.HandleInput("\t")
	}
	if got := header(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("header after cycling filters =\n%s", strings.Join(got, "\n"))
	}
}

func TestTreeSelectRenderUsesConfiguredAppBranchKeys(t *testing.T) {
	treeHelpTestKeybindings(t, map[string][]string{
		"app.tree.foldOrUp":     {"h"},
		"app.tree.unfoldOrDown": {"l"},
	})
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user"},
	}}
	ts := NewTreeSelect("", root)
	header := stripANSI(strings.Join(ts.Render(200)[:4], "\n"))
	if !strings.Contains(header, " · h/l branch · ") {
		t.Fatalf("header missing configured branch keys:\n%q", header)
	}
}

// Upstream TreeHelp wraps whole items: a narrow width starts each item that
// does not fit on a new indented row and never splits "ctrl+x copy".
func TestTreeHelpWrapsWholeItems(t *testing.T) {
	treeHelpTestKeybindings(t, nil)
	lines := treeHelpLines(30)
	var plain []string
	for _, line := range lines {
		p := stripANSI(line)
		if w := widthx.VisibleWidth(p); w > 30 {
			t.Fatalf("help row overflows 30 columns (%d): %q", w, p)
		}
		plain = append(plain, p)
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "  ↑/↓ move · ←/→ page\n") || !strings.Contains(joined, "ctrl+x copy") {
		t.Fatalf("help rows =\n%s", joined)
	}
}

func TestFilterNoToolsHidesToolResultAndSettings(t *testing.T) {
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user"},
		&fakeNodeTagged{id: "tr", label: "tool result", tags: []string{"tool_result"}},
		&fakeNodeTagged{id: "s", label: "settings", tags: []string{"settings"}},
		&fakeNodeTagged{id: "a", label: "assistant"},
	}}
	ts := NewTreeSelect("", root)
	ts.HandleInput("2") // direct key → no-tools
	if ts.filterMode != "no-tools" {
		t.Fatalf("filterMode=%q want no-tools", ts.filterMode)
	}
	ids := rowIDs(ts)
	if slices.Contains(ids, "tr") {
		t.Errorf("tool_result row `tr` should be hidden in no-tools mode; rows=%v", ids)
	}
	if slices.Contains(ids, "s") {
		t.Errorf("settings row `s` should be hidden in no-tools mode; rows=%v", ids)
	}
	if !slices.Contains(ids, "u") || !slices.Contains(ids, "a") {
		t.Errorf("user/assistant rows should be visible; rows=%v", ids)
	}
}

// TestFilterUserOnlyShowsOnlyUserRows: `user-only` mode shows only
// rows tagged `user`. Mirrors upstream tree-selector.ts:308-310.
func TestFilterUserOnlyShowsOnlyUserRows(t *testing.T) {
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user", tags: []string{"user"}},
		&fakeNodeTagged{id: "tr", label: "tool result", tags: []string{"tool_result"}},
		&fakeNodeTagged{id: "a", label: "assistant"},
	}}
	ts := NewTreeSelect("", root)
	ts.HandleInput("3") // direct key → user-only
	if ts.filterMode != "user-only" {
		t.Fatalf("filterMode=%q want user-only", ts.filterMode)
	}
	ids := rowIDs(ts)
	if len(ids) != 1 || !slices.Contains(ids, "u") {
		t.Errorf("user-only: want only [u], got %v", ids)
	}
}

// TestFilterLabeledOnlyShowsLabeledRows: `labeled-only` mode shows
// only rows tagged `labeled`. Mirrors upstream :317-319.
func TestFilterLabeledOnlyShowsLabeledRows(t *testing.T) {
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user"},
		&fakeNodeTagged{id: "lbl", label: "labeled msg", tags: []string{"labeled"}},
		&fakeNodeTagged{id: "a", label: "assistant"},
	}}
	ts := NewTreeSelect("", root)
	ts.HandleInput("4") // direct key → labeled-only
	if ts.filterMode != "labeled-only" {
		t.Fatalf("filterMode=%q want labeled-only", ts.filterMode)
	}
	ids := rowIDs(ts)
	if len(ids) != 1 || !slices.Contains(ids, "lbl") {
		t.Errorf("labeled-only: want only [lbl], got %v", ids)
	}
}

// TestFilterDirectKeys1to5: keys 1-5 jump directly to each mode
// without cycling.
func TestFilterDirectKeys1to5(t *testing.T) {
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user", tags: []string{"user"}},
	}}
	ts := NewTreeSelect("", root)
	cases := []struct {
		key  string
		want string
	}{
		{"1", "default"},
		{"2", "no-tools"},
		{"3", "user-only"},
		{"4", "labeled-only"},
		{"5", "all"},
	}
	for _, tc := range cases {
		ts.HandleInput(tc.key)
		if ts.filterMode != tc.want {
			t.Errorf("key %q: filterMode=%q want %q", tc.key, ts.filterMode, tc.want)
		}
	}
}

// TestFilterControlKeysMatchRuntimeBindings: the live /tree editor-slot path
// sends raw control bytes (Ctrl+D/T/U/L/A) straight to TreeSelect. Accepting
// only the legacy digit shortcuts makes runtime behavior drift from upstream pi.
func TestFilterControlKeysMatchRuntimeBindings(t *testing.T) {
	useTreeKeybindings(t, nil)
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user", tags: []string{"user"}},
	}}
	ts := NewTreeSelect("", root)
	cases := []struct {
		key  string
		want string
	}{
		{"\x04", "default"},      // Ctrl+D
		{"\x14", "no-tools"},     // Ctrl+T
		{"\x15", "user-only"},    // Ctrl+U
		{"\x0c", "labeled-only"}, // Ctrl+L
		{"\x01", "all"},          // Ctrl+A
	}
	for _, tc := range cases {
		ts.HandleInput(tc.key)
		if ts.filterMode != tc.want {
			t.Errorf("key %q: filterMode=%q want %q", tc.key, ts.filterMode, tc.want)
		}
	}
}

// rowIDs returns the ids of visible rows for test assertions.
func rowIDs(ts *TreeSelect) []string {
	ids := make([]string, len(ts.rows))
	for i, r := range ts.rows {
		ids[i] = r.id
	}
	return ids
}

// TestSetInitialCursorPositionsOnCurrentLeaf: /tree opens on the current leaf
// row, not the bottom row, after forking. Regression for the reported bug
// where /tree always opened at the outermost/bottom entry. Mirrors upstream
// TreeSelectorComponent initial selection (tree-selector.ts:143-145).
func TestSetInitialCursorPositionsOnCurrentLeaf(t *testing.T) {
	useTreeKeybindings(t, nil)
	// r → top → {a → {a1, a2}, b}. Flatten: top(0), a(1), a1(2), a2(3), b(4).
	root := &fakeNode{id: "r", kids: []TreeNode{
		&fakeNode{id: "top", label: "TOP", kids: []TreeNode{
			&fakeNode{id: "a", label: "A", kids: []TreeNode{
				&fakeNode{id: "a1", label: "A1"},
				&fakeNode{id: "a2", label: "A2"},
			}},
			&fakeNode{id: "b", label: "B"},
		}},
	}}
	ts := NewTreeSelect("", root)
	if ts.cursor != len(ts.rows)-1 {
		t.Fatalf("default cursor=%d want bottom %d", ts.cursor, len(ts.rows)-1)
	}

	// Current leaf "a1" is a middle row, not the bottom ("b").
	ts.SetInitialCursor("a1", "")
	if got := ts.rows[ts.cursor].id; got != "a1" {
		t.Fatalf("cursor on %q, want a1 (rows=%v)", got, rowIDs(ts))
	}

	// initialSelectedID overrides the current leaf.
	ts.SetInitialCursor("a1", "b")
	if got := ts.rows[ts.cursor].id; got != "b" {
		t.Fatalf("cursor on %q, want b (initialSelectedID override)", got)
	}

	// An unresolvable id leaves the cursor put (upstream fallback = no jump).
	ts.SetInitialCursor("nope", "")
	if got := ts.rows[ts.cursor].id; got != "b" {
		t.Fatalf("cursor on %q, want b unchanged on miss", got)
	}

	// When the target is folded out, walk up to the nearest visible ancestor.
	ts.cursor = 1 // A (foldable: 2 children under a multi-child parent)
	ts.HandleInput("\x1b[1;5D")
	if ids := rowIDs(ts); len(ids) != 3 {
		t.Fatalf("post-fold rows=%v want [top a b]", ids)
	}
	ts.SetInitialCursor("a1", "") // a1 hidden under folded a → nearest visible = a
	if got := ts.rows[ts.cursor].id; got != "a" {
		t.Fatalf("cursor on %q, want a (nearest visible ancestor of folded a1)", got)
	}
}

// linearChainOfLength builds a 1-root linear chain of n entries
// (root + n-1 descendants) for scroll-behavior tests.
func linearChainOfLength(n int) TreeNode {
	if n <= 0 {
		return &fakeNode{id: "r"}
	}
	// Build the chain bottom-up.
	var node TreeNode = &fakeNode{id: fmt.Sprintf("n%d", n-1), label: fmt.Sprintf("N%d", n-1)}
	for i := n - 2; i >= 0; i-- {
		node = &fakeNode{id: fmt.Sprintf("n%d", i), label: fmt.Sprintf("N%d", i), kids: []TreeNode{node}}
	}
	return &fakeNode{id: "r", kids: []TreeNode{node}}
}

// TestFixScrollKeepsCursorVisibleMovingUp asserts that moving the cursor
// upward across the viewport boundary brings the viewport with it.
// Regression: before, fixScroll only handled the cursor crossing the
// bottom edge; moving up off the top left the cursor invisible (the
// "/tree scrolls past view" bug).
func TestFixScrollKeepsCursorVisibleMovingUp(t *testing.T) {
	ts := NewTreeSelect("scroll-up", linearChainOfLength(45))
	if len(ts.rows) < 45 {
		t.Fatalf("expected 45 rows, got %d", len(ts.rows))
	}
	// Initial cursor is at last row; viewport should include it.
	if ts.cursor < ts.scroll || ts.cursor >= ts.scroll+20 {
		t.Fatalf("initial cursor %d outside viewport [%d, %d)", ts.cursor, ts.scroll, ts.scroll+20)
	}
	// Jump cursor to position 5 (above the current viewport).
	ts.cursor = 5
	ts.fixScroll()
	if ts.cursor < ts.scroll || ts.cursor >= ts.scroll+20 {
		t.Errorf("after upward jump cursor=%d viewport=[%d,%d): cursor scrolled past view", ts.cursor, ts.scroll, ts.scroll+20)
	}
}

// TestFixScrollKeepsCursorVisibleMovingDown asserts the same for downward
// motion.
func TestFixScrollKeepsCursorVisibleMovingDown(t *testing.T) {
	ts := NewTreeSelect("scroll-down", linearChainOfLength(45))
	ts.cursor = 0
	ts.scroll = 0
	ts.fixScroll()
	// Now jump down past the window.
	ts.cursor = 30
	ts.fixScroll()
	if ts.cursor < ts.scroll || ts.cursor >= ts.scroll+20 {
		t.Errorf("after downward jump cursor=%d viewport=[%d,%d)", ts.cursor, ts.scroll, ts.scroll+20)
	}
}

// Upstream TreeSelectorComponent shows max(5, floor(terminalHeight / 2)) rows,
// not a fixed 20.
func TestTreeSelectRowBudgetFollowsTheTerminalHeight(t *testing.T) {
	for _, tc := range []struct{ height, want int }{{8, 5}, {30, 15}, {80, 40}} {
		if got := TreeVisibleLines(tc.height); got != tc.want {
			t.Fatalf("TreeVisibleLines(%d) = %d, want %d", tc.height, got, tc.want)
		}
	}
	ts := NewTreeSelect("budget", linearChainOfLength(60))
	ts.MaxVisibleLines = 7
	rendered := 0
	for _, line := range ts.Render(80) {
		if strings.Contains(line, "N") {
			rendered++
		}
	}
	if rendered != 7 {
		t.Fatalf("rendered %d tree rows, want 7", rendered)
	}
}

// TestFilterHidesUsageTaggedRowsInEveryMode: usage entries (cache-warming
// refreshes) never appear, not even in `all` mode. Mirrors upstream
// tree-selector.ts applyFilter.
func TestFilterHidesUsageTaggedRowsInEveryMode(t *testing.T) {
	root := &fakeNodeTagged{id: "r", kids: []TreeNode{
		&fakeNodeTagged{id: "u", label: "user msg", tags: []string{"user"}},
		&fakeNodeTagged{id: "w", label: "usage", tags: []string{"usage"}},
		&fakeNodeTagged{id: "a", label: "assistant"},
	}}
	ts := NewTreeSelect("", root)
	for range 5 {
		for _, r := range ts.rows {
			if r.id == "w" {
				t.Fatalf("usage row visible in %s mode: rows=%v", ts.filterMode, rowPrefixes(ts))
			}
		}
		ts.HandleInput("\t")
	}
}
