package tui

// TreeSelect: single-pane navigable tree selector.
//
// Renders a flattened DFS list of tree nodes with
// parent/child connectors and lets the user pick one (typically used
// by /fork, where the chosen node becomes the new branch point).
//
// Each node carries an opaque ID string; when the user confirms,
// SelectedID() returns that id (empty on cancel).
//
// Keys:
//   ↑/↓, Ctrl+P/Ctrl+N      move cursor
//   Enter                    select
//   Esc                      cancel

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TreeNode is the shape TreeSelect operates on. SessionTreeNode in
// codingagent satisfies this via a thin adapter.
type TreeNode interface {
	NodeID() string
	NodeLabel() string // single-line display, no \n
	NodeChildren() []TreeNode
}

// TreeNodeWithBranchLabel is implemented by adapters that carry a user-set
// branch label. Upstream renders this label outside the entry text as
// theme.fg("warning", "[label] ") (tree-selector.ts:673); it must not be folded
// into NodeLabel(), otherwise it loses its warning colour and selected rows bold
// the label instead of only bolding entry content.
type TreeNodeWithBranchLabel interface {
	// NodeBranchLabel returns the user-set branch label, or "" when none is set.
	NodeBranchLabel() string
}

// TreeNodeWithLabelTimestamp is implemented by adapters that carry
// a user-set branch label with its set-time. When `T` toggles label
// timestamps on, the picker prepends `hh:mm` (or a
// longer form for cross-day stamps) to the row content. Adapters
// that don't implement this interface render with no timestamp -
// equivalent to upstream's silent-empty branch when
// `flatNode.node.labelTimestamp` is undefined
// (tree-selector.ts:678-682).
type TreeNodeWithLabelTimestamp interface {
	// NodeLabelTimestamp returns the upstream wire-format timestamp
	// of the LabelEntry that set the label, or "" when no label is
	// set or no timestamp is available.
	NodeLabelTimestamp() string
}

// TreeNodeWithFilterTags is implemented by adapters that classify nodes for
// filter-mode skip-sets. Settings, tool, user, and labeled tags drive the
// default, no-tools, user-only, labeled-only, and all modes; the usage tag
// hides a node in every mode. Adapters without tags remain visible in every
// mode.
type TreeNodeWithFilterTags interface {
	// NodeFilterTags returns zero or more semantic tags. Stable per
	// node: the picker caches them at flatten time.
	NodeFilterTags() []string
}

// TreeNodeSearchableText is implemented by adapters that expose the
// plain-text a type-to-search query is matched against. Mirrors
// upstream `getSearchableText` (tree-selector.ts:559-600): the label
// plus role, message content, and entry-type fields. Adapters that
// don't implement it are matched on their NodeLabel only.
type TreeNodeSearchableText interface {
	// NodeSearchableText returns the lowercase-matchable text for the
	// node, excluding any ANSI formatting.
	NodeSearchableText() string
}

// TreeSelect overlay. Done()/Cancelled()/SelectedID() mirror the
// FilterableList contract.
type TreeSelect struct {
	invalidatable
	Title string
	// MaxVisibleLines is the number of tree rows shown at once. Upstream
	// TreeSelectorComponent uses max(5, floor(terminalHeight / 2)); see
	// TreeVisibleLines. Zero uses treeWindow.
	MaxVisibleLines int

	// Flattened DFS row-list. Split this into the
	// unconditional `allRows` (computed once at construction) and
	// the visible `rows` (re-derived from allRows whenever
	// foldedNodes changes). On a freshly-constructed picker the
	// two slices are identical until the user folds something.
	allRows []treeRow
	rows    []treeRow
	cursor  int
	scroll  int

	// foldedNodes contains the IDs of nodes whose descendants are
	// hidden via Ctrl+Left / Alt+Left fold. Recomputed-from-source
	// on every change. Mirrors upstream `foldedNodes` at
	// tree-selector.ts:65. Not persisted across /tree re-opens
	// (matches upstream behaviour: the picker is constructed fresh
	// each open so its state is per-session anyway).
	foldedNodes map[string]bool

	// foldableCache memoizes isFoldable per visible row id. Computed
	// once whenever visibility changes (recomputeVisible) so Render's
	// per-row foldability check is O(1). Without it, isFoldable rebuilt
	// O(n) maps and visibleChildrenCount looped all rows calling it,
	// making each Render O(n^2)+: the cause of /tree becoming
	// unresponsive on large sessions.
	foldableCache map[string]bool

	// labelCache / tagsCache memoize the per-node label and filter tags,
	// computed lazily on first use. Upstream's FlatNode stores only the
	// node and formats labels at render time (~20 visible rows); pig used
	// to format every node's markdown label AND unmarshal its message
	// twice during flatten, making /tree slow to open on large sessions.
	// Labels are stable for a tree's lifetime (no live label-edit update),
	// so the caches never need invalidation.
	labelCache map[string]string
	tagsCache  map[string][]string
	// searchCache memoizes each row's searchable lowercase text.
	// Neither labels, tags nor searchable text change for a tree's
	// lifetime (no live label-edit reindex), so the caches never need
	// invalidation.
	searchCache map[string]string

	// showLabelTimestamps toggles inline `hh:mm` (or longer) display
	// of LabelEntry timestamps next to label-prefixed rows. Toggled
	// by `T` (Shift+T per upstream `app.tree.toggleLabelTimestamp`
	// at keybindings.ts:126-129). Defaults off.
	showLabelTimestamps bool

	// filterMode is one of filterModes. Tab cycles forward and Shift+Tab
	// cycles backward. Each /tree open resets it to default.
	filterMode  string
	filterModes []string

	// searchQuery is the active type-to-search string, matched as
	// lowercase whitespace-separated tokens against each node's
	// searchable text. Mirrors upstream `searchQuery`
	// (tree-selector.ts:113). Empty when no search is active.
	searchQuery string

	// Inline label editing. `editingLabel` is true when
	// the user has pressed Shift+L to rename the highlighted row.
	// `labelBuf` accumulates typed characters; Enter commits, Esc
	// cancels. Mirrors upstream `onLabelEdit` flow at
	// tree-selector.ts:985-993 + session-manager.ts:1006-1024.
	editingLabel bool
	labelBuf     string
	// OnLabelEdit is called when the user commits a label (Enter).
	// entryID is the target entry; label is the new name (empty string
	// clears any existing label, matching upstream undefined→delete).
	// The caller is responsible for persisting via Session.AppendLabelChange.
	OnLabelEdit func(entryID, label string)

	done       bool
	cancelled  bool
	selectedID string
}

type treeRow struct {
	id     string
	prefix string   // ASCII tree connectors (gutters + connector cells)
	node   TreeNode // source node; label/tags computed lazily via TreeSelect caches
	depth  int

	// Per-row state needed for fold rendering and
	// timestamp display.
	parentID               string // ID of the row's parent ("" for top-level)
	nChildren              int    // number of direct children in the source tree
	parentMultipleChildren bool   // parent had > 1 children at flatten time
	labelTimestamp         string // upstream wire-format ts; "" when no label

	// Geometry carried over for re-render after fold/unfold so we
	// don't have to re-walk the source tree on every key press.
	displayIndent      int
	showConnector      bool
	isLast             bool
	gutters            []gutterInfo
	isVirtualRootChild bool
}

// gutterInfo describes a vertical-bar continuation column carried
// down from an ancestor that still has more siblings to come.
// Mirrors upstream `GutterInfo` at tree-selector.ts:33-36.
type gutterInfo struct {
	position int  // displayIndent column where the connector was drawn
	show     bool // false → blank cell (ancestor was last sibling)
}

const (
	treeGutterWidth              = 2
	treeWindow                   = 20
	minVisibleAnchorContentWidth = 4
	maxVisibleAnchorContentWidth = 20
	minAnchorContextWidth        = 2
	maxAnchorContextWidth        = 12
)

type horizontalViewportRow struct {
	gutter     string
	body       string
	anchorCol  int
	bodyWidth  int
	isSelected bool
}

func treeVisibleWidth(s string) int {
	return widthx.VisibleWidth(s)
}

// renderTreeHorizontalViewport mirrors upstream tree-selector.ts
// renderHorizontalViewport: keep the two-column cursor gutter visible and pan
// only the row body when the selected row's content anchor would otherwise be
// off-screen in a deep tree.
func renderTreeHorizontalViewport(rows []horizontalViewportRow, width int) []string {
	viewportWidth := max(0, width-treeGutterWidth)
	maxBodyWidth := 0
	for _, row := range rows {
		maxBodyWidth = max(maxBodyWidth, row.bodyWidth)
	}
	maxHorizontalScroll := max(0, maxBodyWidth-viewportWidth)

	horizontalScroll := 0
	if maxHorizontalScroll > 0 {
		for _, row := range rows {
			if !row.isSelected {
				continue
			}
			minVisibleAnchorContent := min(maxVisibleAnchorContentWidth, max(minVisibleAnchorContentWidth, viewportWidth/3))
			if row.anchorCol > viewportWidth-minVisibleAnchorContent {
				anchorContext := min(maxAnchorContextWidth, max(minAnchorContextWidth, viewportWidth/4))
				horizontalScroll = min(maxHorizontalScroll, row.anchorCol-anchorContext)
			}
			break
		}
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		line := row.gutter + row.body
		if horizontalScroll > 0 {
			clipped := widthx.SliceWithWidth(row.body, horizontalScroll, viewportWidth, true)
			body := clipped.Text
			if strings.ContainsRune(body, 0x1B) {
				body += SGRFgReset + SGRBoldDimReset + SGRItalicReset + SGRUnderlineReset
			}
			line = row.gutter + body
		}
		lines = append(lines, padOrTrunc(line, width))
	}
	return lines
}

// NewTreeSelect builds the flattened view from the given root.
// Root itself is not rendered; only its children.
//
// Indent + connector rules mirror upstream
// tree-selector.ts:143-260 (flattenTree) and :631-666 (per-row
// prefix construction). Single-child chains stay at the same
// indent and skip the connector glyph; only branch points (parent
// with > 1 children) bump indent and draw `├─`/`└─`.
// TreeVisibleLines is upstream TreeSelectorComponent's row budget for a
// terminal of the given height.
func TreeVisibleLines(terminalHeight int) int {
	return max(5, terminalHeight/2)
}

func (t *TreeSelect) visibleLines() int {
	if t.MaxVisibleLines > 0 {
		return t.MaxVisibleLines
	}
	return treeWindow
}

func NewTreeSelect(title string, root TreeNode) *TreeSelect {
	t := &TreeSelect{
		Title:       title,
		foldedNodes: make(map[string]bool),
		// Keep the cycle order aligned with the filter-mode hint.
		filterMode:  "default",
		filterModes: []string{"default", "no-tools", "user-only", "labeled-only", "all"},
	}
	if root == nil {
		return t
	}
	roots := root.NodeChildren()
	if len(roots) == 0 {
		return t
	}
	multipleRoots := len(roots) > 1
	for i, r := range roots {
		isLast := i == len(roots)-1
		indent := 0
		justBranched := false
		showConnector := false
		isVirtualRootChild := false
		if multipleRoots {
			indent = 1
			justBranched = true
			showConnector = true
			isVirtualRootChild = true
		}
		t.flatten(r, "", multipleRoots, indent, showConnector, isLast, justBranched, isVirtualRootChild, multipleRoots, nil, 0)
	}
	t.recomputeVisible()
	if len(t.rows) > 0 {
		t.cursor = len(t.rows) - 1
		t.fixScroll()
	}
	return t
}

// SetInitialCursor positions the cursor on the current leaf (or an explicit
// initialSelectedID when given), so /tree opens where the user currently is
// rather than at the bottom row. When the target is filtered or folded out it
// walks up to the nearest visible ancestor. Mirrors upstream
// TreeSelectorComponent's `targetId = initialSelectedId ?? currentLeafId`
// initial selection (tree-selector.ts:143-145). When neither is resolvable the
// cursor keeps NewTreeSelect's last-visible-row default (upstream's fallback).
//
// Must be called after construction (rows are built) and before the first
// render.
func (t *TreeSelect) SetInitialCursor(currentLeafID, initialSelectedID string) {
	target := initialSelectedID
	if target == "" {
		target = currentLeafID
	}
	if idx, ok := t.nearestVisibleIndex(target); ok {
		t.cursor = idx
		t.fixScroll()
	}
}

// nearestVisibleIndex returns the index in the visible rows of entryID, or of
// its nearest visible ancestor when entryID is itself hidden. Mirrors upstream
// findNearestVisibleIndex (tree-selector.ts): walk up parent links until a
// visible row is found. Returns ok=false when nothing on the path is visible.
func (t *TreeSelect) nearestVisibleIndex(entryID string) (int, bool) {
	if entryID == "" || len(t.rows) == 0 {
		return 0, false
	}
	parentByID := make(map[string]string, len(t.allRows))
	for _, r := range t.allRows {
		parentByID[r.id] = r.parentID
	}
	visibleIdx := make(map[string]int, len(t.rows))
	for i, r := range t.rows {
		visibleIdx[r.id] = i
	}
	cur := entryID
	for cur != "" {
		if idx, ok := visibleIdx[cur]; ok {
			return idx, true
		}
		next, ok := parentByID[cur]
		if !ok {
			break
		}
		cur = next
	}
	return 0, false
}

// flatten descends `n` and appends one row per visited node.
//
//   - indent             logical indent level (in 3-char cells)
//   - showConnector      true → draw `├─`/`└─` at indent-1; false → flat (3 blanks)
//   - isLast             this node is the last sibling at its branch
//   - justBranched       parent rendered AT a branch point (multipleChildren)
//   - isVirtualRootChild this node is a top-level root under a virtual multi-root
//   - multipleRoots      session has > 1 root (shifts displayIndent by -1)
//   - gutters            ancestor continuation columns
//   - depth              vestigial; kept for parity with old API consumers
//
// Acceptance map to upstream:
//
//   - Indent rule:        :226-241 (`childIndent` switch)
//   - Connector rule:     :255 (`showConnector = multipleChildren`)
//   - Gutter carry:       :247-253 (push only when connector displayed)
//   - Prefix render:      :631-666 (char grid at displayIndent*3)
//   - displayIndent:      :629 (multipleRoots ? max(0, indent-1) : indent)
func (t *TreeSelect) flatten(n TreeNode, parentID string, parentMultipleChildren bool, indent int, showConnector, isLast, justBranched, isVirtualRootChild, multipleRoots bool, gutters []gutterInfo, depth int) {
	displayIndent := indent
	if multipleRoots {
		displayIndent = max(indent-1, 0)
	}

	kids := n.NodeChildren()
	labelTs := ""
	if lt, ok := n.(TreeNodeWithLabelTimestamp); ok {
		labelTs = lt.NodeLabelTimestamp()
	}
	rowGutters := append([]gutterInfo(nil), gutters...) // defensive copy
	t.allRows = append(t.allRows, treeRow{
		id:                     n.NodeID(),
		prefix:                 buildTreePrefix(displayIndent, showConnector && !isVirtualRootChild, isLast, gutters),
		node:                   n,
		depth:                  depth,
		parentID:               parentID,
		nChildren:              len(kids),
		parentMultipleChildren: parentMultipleChildren,
		labelTimestamp:         labelTs,
		displayIndent:          displayIndent,
		showConnector:          showConnector && !isVirtualRootChild,
		isLast:                 isLast,
		gutters:                rowGutters,
		isVirtualRootChild:     isVirtualRootChild,
	})

	if len(kids) == 0 {
		return
	}
	multipleChildren := len(kids) > 1

	// Child indent (mirrors upstream :226-241).
	var childIndent int
	switch {
	case multipleChildren:
		childIndent = indent + 1 // branch point: +1
	case justBranched && indent > 0:
		childIndent = indent + 1 // first generation after a branch: +1 visual grouping
	default:
		childIndent = indent // single-child chain: stay flat
	}

	// Build child gutters (mirrors upstream :243-253). Only add a
	// gutter entry when this node's connector was actually drawn
	// (suppressed for virtual-root children and for flat rows).
	connectorDisplayed := showConnector && !isVirtualRootChild
	childGutters := gutters
	if connectorDisplayed {
		connectorPosition := max(displayIndent-1, 0)
		next := make([]gutterInfo, 0, len(gutters)+1)
		next = append(next, gutters...)
		next = append(next, gutterInfo{position: connectorPosition, show: !isLast})
		childGutters = next
	}

	for i, c := range kids {
		childIsLast := i == len(kids)-1
		t.flatten(c, n.NodeID(), multipleChildren, childIndent, multipleChildren, childIsLast, multipleChildren, false, multipleRoots, childGutters, depth+1)
	}
}

// buildTreePrefix renders the upstream char-grid prefix
// (tree-selector.ts:631-666 minus fold markers and active-path
// bullets, which pig does not yet support). Width = displayIndent
// × 3. Each level is either a gutter cell ("│  " or "   "), the
// connector cell ("├─ "/"└─ "), or three blanks.
func buildTreePrefix(displayIndent int, showConnector, isLast bool, gutters []gutterInfo) string {
	if displayIndent <= 0 {
		// Top-level row in single-root case: no leading prefix.
		return ""
	}
	connectorPosition := -1
	if showConnector {
		connectorPosition = displayIndent - 1
	}
	var b strings.Builder
	b.Grow(displayIndent * 3)
	for level := range displayIndent {
		if g, ok := gutterAt(gutters, level); ok {
			if g.show {
				b.WriteString("│  ")
			} else {
				b.WriteString("   ")
			}
			continue
		}
		if level == connectorPosition {
			if isLast {
				b.WriteString("└─ ")
			} else {
				b.WriteString("├─ ")
			}
			continue
		}
		b.WriteString("   ")
	}
	return b.String()
}

func gutterAt(gutters []gutterInfo, level int) (gutterInfo, bool) {
	for _, g := range gutters {
		if g.position == level {
			return g, true
		}
	}
	return gutterInfo{}, false
}

// usageRows returns the rows every filter mode hides: usage entries such as
// cache-warming refreshes. Mirrors upstream tree-selector.ts applyFilter.
func (t *TreeSelect) usageRows() map[string]bool {
	skip := make(map[string]bool)
	for _, r := range t.allRows {
		if slices.Contains(t.rowTags(r), "usage") {
			skip[r.id] = true
		}
	}
	return skip
}

// recomputeVisible rebuilds `t.rows` from `t.allRows` by stripping
// every descendant of any node in `t.foldedNodes`. Mirrors upstream
// `applyFilter`'s fold-filter pass at tree-selector.ts:343-352.
// Cursor is clamped to the new visible range.
func (t *TreeSelect) recomputeVisible() {
	// Two-pass filter (matches upstream tree-selector.ts:282-352
	// pre-recomputeVisualStructure):
	//
	//   1. Filter-mode skip: rows with `settings` tag dropped in
	//      `default` mode; everything kept in `all` mode.
	//   2. Fold skip: rows whose ancestor is folded are dropped.
	//
	// We cache the filter-mode skip-set keyed by id so we can use it
	// to drive both the row drop AND the fold-ancestor lookup
	// (because folded nodes themselves may be filter-mode hidden,
	// in which case their descendants should still be hidden by
	// fold transitively: mirrors upstream `skipSet` logic at
	// :349-352).
	filterSkip := t.usageRows()
	switch t.filterMode {
	case "default":
		for _, r := range t.allRows {
			if slices.Contains(t.rowTags(r), "settings") {
				filterSkip[r.id] = true
			}
		}
	case "no-tools":
		// Default minus tool-result rows. Mirrors upstream
		// tree-selector.ts:314-316.
		for _, r := range t.allRows {
			if slices.Contains(t.rowTags(r), "settings") ||
				slices.Contains(t.rowTags(r), "tool_result") {
				filterSkip[r.id] = true
			}
		}
	case "user-only":
		// Only user-message rows. Mirrors upstream :308-310.
		for _, r := range t.allRows {
			if !slices.Contains(t.rowTags(r), "user") {
				filterSkip[r.id] = true
			}
		}
	case "labeled-only":
		// Only rows that carry a user-set label. Mirrors upstream :317-319.
		for _, r := range t.allRows {
			if !slices.Contains(t.rowTags(r), "labeled") {
				filterSkip[r.id] = true
			}
		}
		// "all": nothing skipped
	}

	// Type-to-search filter, mirroring upstream tree-selector.ts:337
	// and :390-393. Tokens are the lowercase query split on whitespace;
	// a row survives only if every token is a substring of its
	// searchable text. Applying it here keeps recomputeVisible the
	// single place filter/fold/search visibility is re-derived.
	searchSkip := make(map[string]bool)
	if t.searchQuery != "" {
		tokens := strings.Fields(strings.ToLower(t.searchQuery))
		if len(tokens) > 0 {
			for _, r := range t.allRows {
				text := t.rowSearchText(r)
				ok := true
				for _, tok := range tokens {
					if !strings.Contains(text, tok) {
						ok = false
						break
					}
				}
				if !ok {
					searchSkip[r.id] = true
				}
			}
		}
	}

	t.rows = t.rows[:0]
	foldSkip := make(map[string]bool)
	for _, r := range t.allRows {
		if r.parentID != "" && (t.foldedNodes[r.parentID] || foldSkip[r.parentID]) {
			foldSkip[r.id] = true
			continue
		}
		if filterSkip[r.id] || searchSkip[r.id] {
			continue
		}
		t.rows = append(t.rows, r)
	}
	if t.cursor >= len(t.rows) {
		t.cursor = len(t.rows) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
	t.computeFoldable()
	t.fixScroll()
}

// rowLabel returns the node's rendered label, computing it lazily and
// memoizing per node id. Mirrors upstream's render-time label formatting
// (only visible rows are ever formatted in practice).
func (t *TreeSelect) rowLabel(r treeRow) string {
	if v, ok := t.labelCache[r.id]; ok {
		return v
	}
	var v string
	if r.node != nil {
		v = r.node.NodeLabel()
	}
	if t.labelCache == nil {
		t.labelCache = make(map[string]string)
	}
	t.labelCache[r.id] = v
	return v
}

// rowTags returns the node's filter tags, computed lazily and memoized.
// Only touched when a filter mode is active.
func (t *TreeSelect) rowTags(r treeRow) []string {
	if v, ok := t.tagsCache[r.id]; ok {
		return v
	}
	var tags []string
	if ft, ok := r.node.(TreeNodeWithFilterTags); ok {
		tags = ft.NodeFilterTags()
	}
	if t.tagsCache == nil {
		t.tagsCache = make(map[string][]string)
	}
	t.tagsCache[r.id] = tags
	return tags
}

// rowSearchText returns the node's lowercase searchable text, memoized.
// Mirrors the searchable-text sourcing in upstream getSearchableText
// (tree-selector.ts:559-600) via the TreeNodeSearchableText adapter.
// Only touched when a search query is active.
func (t *TreeSelect) rowSearchText(r treeRow) string {
	if v, ok := t.searchCache[r.id]; ok {
		return v
	}
	v := strings.ToLower(r.node.NodeLabel())
	if st, ok := r.node.(TreeNodeSearchableText); ok {
		v = strings.ToLower(st.NodeSearchableText())
	}
	if t.searchCache == nil {
		t.searchCache = make(map[string]string)
	}
	t.searchCache[r.id] = v
	return v
}

// computeFoldable memoizes foldability for every visible row in O(n*depth).
// Called once per visibility change (recomputeVisible). Mirrors upstream
// tree-selector.ts:1010-1018 foldability but precomputed so Render's
// per-row lookups are O(1) instead of rebuilding O(n) maps each call.
func (t *TreeSelect) computeFoldable() {
	parentByID := make(map[string]string, len(t.allRows))
	for _, r := range t.allRows {
		parentByID[r.id] = r.parentID
	}
	visible := make(map[string]bool, len(t.rows))
	for _, r := range t.rows {
		visible[r.id] = true
	}
	// Nearest visible ancestor for each visible row.
	visibleParentOf := make(map[string]string, len(t.rows))
	for _, r := range t.rows {
		cur := parentByID[r.id]
		for cur != "" && !visible[cur] {
			cur = parentByID[cur]
		}
		visibleParentOf[r.id] = cur
	}
	// Count of visible rows under each visible parent.
	childCount := make(map[string]int, len(t.rows))
	for _, r := range t.rows {
		if vp := visibleParentOf[r.id]; vp != "" {
			childCount[vp]++
		}
	}
	cache := make(map[string]bool, len(t.rows))
	for _, r := range t.rows {
		if r.nChildren == 0 {
			continue
		}
		vp := visibleParentOf[r.id]
		cache[r.id] = vp == "" || childCount[vp] > 1
	}
	t.foldableCache = cache
}

// isFoldable mirrors upstream tree-selector.ts:1010-1018: a node is
// foldable iff it has visible children AND either has no visible
// parent (top-level row / virtual-root child) or its visible parent
// has multiple visible children (segment start under a branch point).
// Reads the foldableCache built by computeFoldable.
func (t *TreeSelect) isFoldable(row treeRow) bool {
	return t.foldableCache[row.id]
}

// formatLabelTimestamp mirrors upstream tree-selector.ts:787-810.
// Same-day  → `hh:mm`
// Same-year → `M/D hh:mm`
// Other     → `YY/M/D hh:mm`
// Returns "" when ts can't be parsed.
func formatLabelTimestamp(ts string, now time.Time) string {
	if ts == "" {
		return ""
	}
	// LabelEntry timestamps are persisted via Go's RFC3339-ish form
	// from coding/session.go's Now() helper. Try a few common forms.
	var t time.Time
	var err error
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z"} {
		t, err = time.Parse(layout, ts)
		if err == nil {
			break
		}
	}
	if err != nil {
		return ""
	}
	t = t.Local()
	hhmm := fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return hhmm
	}
	if t.Year() == now.Year() {
		return fmt.Sprintf("%d/%d %s", int(t.Month()), t.Day(), hhmm)
	}
	return fmt.Sprintf("%02d/%d/%d %s", t.Year()%100, int(t.Month()), t.Day(), hhmm)
}

// Done / Cancelled / SelectedID implement the modal contract.
func (t *TreeSelect) Done() bool         { return t.done }
func (t *TreeSelect) Cancelled() bool    { return t.cancelled }
func (t *TreeSelect) SelectedID() string { return t.selectedID }

// Render draws header + visible rows.
func (t *TreeSelect) Render(width int) []string {
	// Every row is padded or truncated to width, so narrow renders stay
	// within the terminal instead of overflowing a floor.
	width = max(width, 0)
	// When editing a label, show the input prompt in the header
	// instead of the normal hint. Mirrors upstream label-edit UI at
	// tree-selector.ts:990-993 which focuses the selector's inline input.
	//
	// Otherwise the header mirrors upstream tree-selector.ts:
	//   ─── (top border, DynamicBorder)
	//   ␣␣Session Tree     (bold)
	//   TreeHelp rows      (chunk-aware wrapped key help)
	//   ␣␣Type to search:
	//   ─── (bottom border)
	//
	// The active type-to-search query renders on the search line after a
	// muted "Type to search:" label, mirroring upstream SearchLine.
	headerHint := treeHelpLines(width)
	if t.editingLabel {
		headerHint = []string{fmt.Sprintf("  Label: %s\u2588  (Enter commit · Esc cancel)", t.labelBuf)}
	}
	sep := strings.Repeat("─", width)
	// The /tree overlay frame is drawn in the Border color (upstream renders
	// it via the box component), not faint gray.
	border := fg(ActiveTheme().Border, sep)
	// "Type to search:" is muted, mirroring upstream tree-selector.ts:1169-1171;
	// the active query renders in accent after it.
	searchLine := "  " + fg(ActiveTheme().Muted, "Type to search:")
	if t.searchQuery != "" {
		searchLine += " " + fg(ActiveTheme().Accent, t.searchQuery)
	}
	lines := []string{
		padOrTrunc(border, width),
		padOrTrunc("   \033[1mSession Tree\033[0m", width),
	}
	for _, line := range headerHint {
		lines = append(lines, padOrTrunc(line, width))
	}
	lines = append(lines, padOrTrunc(searchLine, width), padOrTrunc(border, width))
	if len(t.rows) == 0 {
		lines = append(lines, padOrTrunc("", width))
		lines = append(lines, padOrTrunc(fg(ActiveTheme().Muted, "  No entries found"), width))
		lines = append(lines, padOrTrunc(fg(ActiveTheme().Muted, "  (0/0)"), width))
		lines = append(lines, padOrTrunc(border, width))
		return lines
	}
	// Spacer before the items, mirroring upstream Spacer(1).
	lines = append(lines, padOrTrunc("", width))
	now := time.Now()
	// Cap rendered rows to match the scroll window used by fixScroll.
	// Without this cap the tree could emit up to 50 rows which overflow
	// the editor slot when the chat container reserves the rest of the
	// viewport, leaving the cursor stranded above the visible area.
	maxRows := min(t.visibleLines(), len(t.rows))
	viewportRows := make([]horizontalViewportRow, 0, maxRows)
	for i := range maxRows {
		idx := t.scroll + i
		if idx >= len(t.rows) {
			break
		}
		r := t.rows[idx]
		// Upstream composes each line as cursor + prefix + foldMarker +
		// activePath bullet + content. pig does not yet track the full
		// active-path set, but the current visible branch is the active
		// path in the scenarios we cover here, so every visible row keeps
		// the `• ` marker.
		cursor := "  "
		isSelected := idx == t.cursor
		if isSelected {
			// Space inside the accent span, mirroring upstream's
			// `theme.fg("accent", "› ")`.
			cursor = fg(ActiveTheme().Accent, "› ")
		}

		// Fold marker. When the row's connector is
		// shown AND the node is foldable / folded, the connector's
		// middle char is replaced by `⊞` (folded) or `⊟`
		// (unfoldable+expanded), mirroring upstream
		// tree-selector.ts:655-661. When no connector is shown
		// (single-child chain / root) but the node IS folded, the
		// upstream foldMarker `⊞ ` is prepended to the label
		// (tree-selector.ts:670-672).
		prefix := r.prefix
		foldable := t.isFoldable(r)
		folded := t.foldedNodes[r.id]
		if r.showConnector && (foldable || folded) {
			prefix = swapConnectorMiddle(prefix, folded, foldable)
		}
		foldMarker := ""
		if folded && !r.showConnector {
			foldMarker = "⊞ "
		}

		// User-set branch label and optional inline timestamp.
		// Upstream renders the branch label separately from entry content as
		// `theme.fg("warning", "[label] ")` (tree-selector.ts:673). Keeping it
		// out of rowLabel is what preserves the warning colour and prevents
		// selected-row bold from applying to the label.
		branchLabel := ""
		if bn, ok := r.node.(TreeNodeWithBranchLabel); ok {
			if s := bn.NodeBranchLabel(); s != "" {
				branchLabel = fg(ActiveTheme().Warning, "["+s+"] ")
			}
		}
		labelTs := ""
		if t.showLabelTimestamps && r.labelTimestamp != "" {
			if s := formatLabelTimestamp(r.labelTimestamp, now); s != "" {
				labelTs = dim(s) + " "
			}
		}

		// Path-marker bullet is accent-coloured, mirroring upstream's
		// `theme.fg("accent", "• ")` pathMarker. Selected-row content is
		// bolded (upstream `isSelected ? theme.bold(result)`), closed with
		// the scoped intensity reset (SGR 22) so the selected-row background
		// survives.
		pathMarker := fg(ActiveTheme().Accent, "• ")
		prefixPart := fg(ActiveTheme().Dim, prefix) + foldMarker + pathMarker
		label := t.rowLabel(r)
		if isSelected {
			label = "\x1b[1m" + label + SGRBoldDimReset
		}
		body := prefixPart + branchLabel + labelTs + label
		viewportRows = append(viewportRows, horizontalViewportRow{
			gutter:     cursor,
			body:       body,
			anchorCol:  treeVisibleWidth(prefixPart),
			bodyWidth:  treeVisibleWidth(body),
			isSelected: isSelected,
		})
	}
	for i, padded := range renderTreeHorizontalViewport(viewportRows, width) {
		if viewportRows[i].isSelected {
			// Close the selected-row background with a scoped bg reset
			// (SGR 49), mirroring upstream theme.bg's `${ansi}${line}\x1b[49m`.
			// The row's interior fg styling already closes with SGR 39, so
			// the highlight spans the full width without a full `\x1b[0m`
			// clearing the background early.
			padded = ActiveTheme().SelectedBg + padded + SGRBgReset
		}
		lines = append(lines, padded)
	}
	lines = append(lines, padOrTrunc(fg(ActiveTheme().Muted, fmt.Sprintf("  (%d/%d)", t.cursor+1, len(t.rows))), width))
	lines = append(lines, padOrTrunc("", width))
	lines = append(lines, padOrTrunc(border, width))
	return lines
}

// findBranchSegmentStart returns the cursor index of the start of
// the current "branch segment" in the given direction. Mirrors
// upstream tree-selector.ts:1027-1060.
//
// Up direction: walk visible parents until we hit a multi-child
// branch point. The current child of that branch point IS the
// segment start; return its index, but only if we've actually
// moved up (mirrors upstream's `segmentStart < selectedIndex`
// guard: when we're already AT a segment start, keep climbing).
//
// Down direction: walk first-children until we hit a leaf or a
// multi-child node. At a leaf, return that leaf's index. At a
// multi-child node, return its FIRST child's index.
//
// On linear chains (no multi-child ancestors above; no multi-child
// descendants below) both directions return the cursor unchanged.
// HandleInput's caller falls back to a one-row move in that case so
// the keys still feel like simple navigation.
func (t *TreeSelect) findBranchSegmentStart(direction string) int {
	if len(t.rows) == 0 {
		return 0
	}
	selectedID := t.rows[t.cursor].id
	if selectedID == "" {
		return t.cursor
	}

	// Build visible-id→index, parent, children maps from t.rows
	// (the post-fold-filter visible list: folded subtrees do not
	// participate, matching upstream's `visibleChildrenMap`).
	indexByID := make(map[string]int, len(t.rows))
	parentByID := make(map[string]string, len(t.rows))
	childrenByID := make(map[string][]string, len(t.rows))
	for i, r := range t.rows {
		indexByID[r.id] = i
		parentByID[r.id] = r.parentID
		if r.parentID != "" {
			childrenByID[r.parentID] = append(childrenByID[r.parentID], r.id)
		}
	}

	currentID := selectedID
	if direction == "down" {
		for {
			children := childrenByID[currentID]
			if len(children) == 0 {
				return indexByID[currentID]
			}
			if len(children) > 1 {
				return indexByID[children[0]]
			}
			currentID = children[0]
		}
	}

	// direction == "up"
	for {
		parentID, hasParent := parentByID[currentID]
		if !hasParent || parentID == "" {
			return indexByID[currentID]
		}
		children := childrenByID[parentID]
		if len(children) > 1 {
			segStart := indexByID[currentID]
			if segStart < t.cursor {
				return segStart
			}
		}
		currentID = parentID
	}
}

// swapConnectorMiddle replaces the `─` (rune index 1) inside a
// `├─ ` / `└─ ` connector with `⊞` (folded) or `⊟`
// (unfoldable+expanded). The connector is always the LAST 3 runes
// of the prefix per buildTreePrefix's grid layout. Mirrors upstream
// tree-selector.ts:655-661.
func swapConnectorMiddle(prefix string, folded, foldable bool) string {
	runes := []rune(prefix)
	if len(runes) < 3 {
		return prefix
	}
	// The connector occupies runes[len-3..len-1]: e.g. `└`, `─`, ` `.
	// Confirm rune len-2 is the dash before swapping (defensive
	// against future prefix-grid changes).
	if runes[len(runes)-2] != '─' {
		return prefix
	}
	switch {
	case folded:
		runes[len(runes)-2] = '⊞'
	case foldable:
		runes[len(runes)-2] = '⊟'
	}
	return string(runes)
}

// HandleInput moves the cursor or commits the selection.
//
// Ctrl+Left/Alt+Left folds the highlighted branch or moves to the current
// branch segment start. Ctrl+Right/Alt+Right unfolds it or moves to the next
// branch segment. Shift+T toggles label timestamps.
func (t *TreeSelect) HandleInput(data string) {
	// When in label-edit mode, intercept all keystrokes
	// for the inline editor instead of the normal navigation handlers.
	if t.editingLabel {
		t.handleLabelInput(data)
		t.Invalidate()
		return
	}
	// Route every action through the keybinding registry, which the coding
	// agent fills with upstream's merged tui.* and app.* table, so user
	// rebinding reaches the app.tree.* actions here. Mirrors upstream
	// tree-selector.ts handleInput.
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectCancel):
		// Esc with an active search clears the query instead of
		// closing the picker, mirroring upstream tree-selector.ts:1032-1035.
		if t.searchQuery != "" {
			t.searchQuery = ""
			t.recomputeVisible()
			break
		}
		t.cancelled = true
		t.done = true
	case kb.Matches(data, KBSelectConfirm):
		if len(t.rows) > 0 && t.cursor < len(t.rows) {
			t.selectedID = t.rows[t.cursor].id
			t.done = true
		}
	case kb.Matches(data, KBSelectUp):
		if t.cursor > 0 {
			t.cursor--
			t.fixScroll()
		}
	case kb.Matches(data, KBSelectDown):
		if t.cursor < len(t.rows)-1 {
			t.cursor++
			t.fixScroll()
		}
	case kb.Matches(data, KBEditorCursorLeft) || kb.Matches(data, KBSelectPageUp):
		t.cursor -= t.visibleLines()
		if t.cursor < 0 {
			t.cursor = 0
		}
		t.fixScroll()
	case kb.Matches(data, KBEditorCursorRight) || kb.Matches(data, KBSelectPageDown):
		t.cursor += t.visibleLines()
		if t.cursor >= len(t.rows) {
			t.cursor = len(t.rows) - 1
		}
		t.fixScroll()
	case kb.Matches(data, "app.tree.foldOrUp"):
		if len(t.rows) == 0 {
			break
		}
		cur := t.rows[t.cursor]
		if t.isFoldable(cur) && !t.foldedNodes[cur.id] {
			t.foldedNodes[cur.id] = true
			t.recomputeVisible()
		} else {
			t.cursor = t.findBranchSegmentStart("up")
			t.fixScroll()
		}
	case kb.Matches(data, "app.tree.unfoldOrDown"):
		if len(t.rows) == 0 {
			break
		}
		cur := t.rows[t.cursor]
		if t.foldedNodes[cur.id] {
			delete(t.foldedNodes, cur.id)
			t.recomputeVisible()
		} else {
			t.cursor = t.findBranchSegmentStart("down")
			t.fixScroll()
		}
	case kb.Matches(data, "app.tree.filter.default"):
		t.setFilterMode("default")
	case kb.Matches(data, "app.tree.filter.noTools"):
		t.setFilterMode("no-tools")
	case kb.Matches(data, "app.tree.filter.userOnly"):
		t.setFilterMode("user-only")
	case kb.Matches(data, "app.tree.filter.labeledOnly"):
		t.setFilterMode("labeled-only")
	case kb.Matches(data, "app.tree.filter.all"):
		t.setFilterMode("all")
	case kb.Matches(data, "app.tree.filter.cycleBackward"):
		t.cycleFilterMode(-1)
	case kb.Matches(data, "app.tree.filter.cycleForward"):
		t.cycleFilterMode(+1)
	case data == "1":
		t.setFilterMode("default")
	case data == "2":
		t.setFilterMode("no-tools")
	case data == "3":
		t.setFilterMode("user-only")
	case data == "4":
		t.setFilterMode("labeled-only")
	case data == "5":
		t.setFilterMode("all")
	case kb.Matches(data, "app.tree.editLabel"):
		if len(t.rows) > 0 && t.cursor < len(t.rows) && t.OnLabelEdit != nil {
			t.labelBuf = t.rowLabel(t.rows[t.cursor])
			t.editingLabel = true
		}
	case kb.Matches(data, "app.tree.toggleLabelTimestamp"):
		t.showLabelTimestamps = !t.showLabelTimestamps
	case kb.Matches(data, KBInputTab): // Tab: cycle filter mode forward
		t.cycleFilterMode(+1)
	case data == "\x1b[Z": // Shift+Tab: cycle filter mode backward
		t.cycleFilterMode(-1)
	case kb.Matches(data, KBEditorDeleteCharBack):
		// Backspace removes the last character of the search query,
		// mirroring upstream tree-selector.ts:1078-1082.
		if t.searchQuery != "" {
			_, sz := lastRuneSize(t.searchQuery)
			t.searchQuery = t.searchQuery[:len(t.searchQuery)-sz]
			t.foldedNodes = map[string]bool{}
			t.recomputeVisible()
		}
	default:
		// Type-to-search: any printable rune appends to the query.
		// Mirrors upstream tree-selector.ts:1104-1110. Control codes
		// fall through silently, matching the label-edit guard.
		hasControl := false
		for _, r := range data {
			code := int(r)
			if code < 32 || code == 0x7f || (code >= 0x80 && code <= 0x9f) {
				hasControl = true
				break
			}
		}
		if !hasControl && data != "" {
			t.searchQuery += data
			t.foldedNodes = map[string]bool{}
			t.recomputeVisible()
		}
	}
	t.Invalidate()
}

// handleLabelInput processes a keystroke while in label-edit mode.
// Enter commits the label; Esc cancels. All other printable characters
// are appended; backspace removes the last rune.
func (t *TreeSelect) handleLabelInput(data string) {
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectConfirm): // Enter: commit
		if len(t.rows) > 0 && t.cursor < len(t.rows) && t.OnLabelEdit != nil {
			t.OnLabelEdit(t.rows[t.cursor].id, t.labelBuf)
		}
		t.editingLabel = false
		t.labelBuf = ""
	case kb.Matches(data, KBSelectCancel): // Esc: cancel
		t.editingLabel = false
		t.labelBuf = ""
	case data == "\x7f" || data == "\b": // Backspace: remove last rune
		if len(t.labelBuf) > 0 {
			_, sz := lastRuneSize(t.labelBuf)
			t.labelBuf = t.labelBuf[:len(t.labelBuf)-sz]
		}
	default:
		// Accept printable characters (guard: skip raw control codes).
		hasControl := false
		for _, r := range data {
			code := int(r)
			if code < 32 || code == 0x7f || (code >= 0x80 && code <= 0x9f) {
				hasControl = true
				break
			}
		}
		if !hasControl {
			t.labelBuf += data
		}
	}
}

// cycleFilterMode advances `filterMode` by `direction` (+1 forward,
// -1 backward) within `filterModes`, then re-derives the visible row
// list. Mirrors upstream `app.tree.filter.cycleForward` /
// `cycleBackward` at `tree-selector.ts:940-983`. Tries to preserve
// the cursor on the same node id across the transition; if the row
// disappears under the new mode (e.g. cursor on a label row that
// `default` mode hides), recomputeVisible's bounds-clamp lands the
// cursor on the nearest still-visible row.
func (t *TreeSelect) cycleFilterMode(direction int) {
	if len(t.filterModes) <= 1 {
		return
	}
	curID := ""
	if t.cursor >= 0 && t.cursor < len(t.rows) {
		curID = t.rows[t.cursor].id
	}
	idx := 0
	for i, m := range t.filterModes {
		if m == t.filterMode {
			idx = i
			break
		}
	}
	next := (idx + direction + len(t.filterModes)) % len(t.filterModes)
	t.filterMode = t.filterModes[next]
	t.recomputeVisible()
	if curID != "" {
		for i, r := range t.rows {
			if r.id == curID {
				t.cursor = i
				t.fixScroll()
				break
			}
		}
	}
}

// setFilterMode switches to the named mode and recomputes visible rows,
// preserving the cursor on the same id when still visible. Mirrors the
// toggle-then-recompute pattern at upstream tree-selector.ts:940-983.
// No-ops if mode is already active.
func (t *TreeSelect) setFilterMode(mode string) {
	if t.filterMode == mode {
		return
	}
	curID := ""
	if t.cursor >= 0 && t.cursor < len(t.rows) {
		curID = t.rows[t.cursor].id
	}
	t.filterMode = mode
	t.recomputeVisible()
	if curID != "" {
		for i, r := range t.rows {
			if r.id == curID {
				t.cursor = i
				t.fixScroll()
				break
			}
		}
	}
}

// fixScroll keeps the cursor inside the visible window. Mirrors upstream
// tree-selector.ts:611-619 which centers the selection within
// maxVisibleLines and clamps to valid range.
func (t *TreeSelect) fixScroll() {
	window := t.visibleLines()
	if len(t.rows) <= window {
		t.scroll = 0
		return
	}
	// Upstream approach: startIndex = max(0, min(
	//   selectedIndex - floor(maxVisibleLines/2),
	//   filteredNodes.length - maxVisibleLines
	// )).
	// This always centers the cursor, giving a consistent scroll
	// experience in both directions.
	desired := max(t.cursor-window/2, 0)
	maxScroll := max(len(t.rows)-window, 0)
	if desired > maxScroll {
		desired = maxScroll
	}
	t.scroll = desired
}

// lastRuneSize returns the last rune in s and its byte size.
// Returns (0,0) for empty string.
func lastRuneSize(s string) (rune, int) {
	if s == "" {
		return 0, 0
	}
	r, sz := utf8.DecodeLastRuneInString(s)
	return r, sz
}

// treeHelpItems mirrors upstream TREE_HELP_ITEMS (tree-selector.ts).
var treeHelpItems = []struct {
	keys       []string
	label      string
	labelFirst bool
}{
	{keys: []string{KBSelectUp, KBSelectDown}, label: "move"},
	{keys: []string{KBEditorCursorLeft, KBEditorCursorRight}, label: "page"},
	{keys: []string{"app.tree.foldOrUp", "app.tree.unfoldOrDown"}, label: "branch"},
	{keys: []string{"app.message.copy"}, label: "copy"},
	{keys: []string{"app.tree.editLabel"}, label: "label"},
	{keys: []string{"app.tree.toggleLabelTimestamp"}, label: "label time"},
	{keys: []string{
		"app.tree.filter.default", "app.tree.filter.noTools", "app.tree.filter.userOnly",
		"app.tree.filter.labeledOnly", "app.tree.filter.all",
	}, label: "filters", labelFirst: true},
	{keys: []string{"app.tree.filter.cycleForward", "app.tree.filter.cycleBackward"}, label: "cycle", labelFirst: true},
}

var treeHelpKeyWords = []struct {
	pattern *regexp.Regexp
	text    string
}{
	{regexp.MustCompile(`\bpageUp\b`), "pgup"},
	{regexp.MustCompile(`\bpageDown\b`), "pgdn"},
	{regexp.MustCompile(`\bup\b`), "↑"},
	{regexp.MustCompile(`\bdown\b`), "↓"},
	{regexp.MustCompile(`\bleft\b`), "←"},
	{regexp.MustCompile(`\bright\b`), "→"},
}

// formatTreeHelpKeys mirrors upstream formatHelpKeys: the first key of each
// binding, compacted under a shared modifier prefix, with arrow names drawn
// as glyphs.
func formatTreeHelpKeys(bindings []string) string {
	var keys []string
	for _, binding := range bindings {
		if bound := GetTUIKeybindings().GetKeys(binding); len(bound) > 0 {
			keys = append(keys, bound[0])
		}
	}
	if len(keys) == 0 {
		return ""
	}
	text := FormatKeyText(compactTreeHelpKeys(keys), false)
	for _, word := range treeHelpKeyWords {
		text = word.pattern.ReplaceAllString(text, word.text)
	}
	return text
}

// compactTreeHelpKeys mirrors upstream compactRawKeys: "ctrl+d/ctrl+t" becomes
// "ctrl+d/t" when every key shares the same modifier prefix.
func compactTreeHelpKeys(keys []string) string {
	if len(keys) == 1 {
		return keys[0]
	}
	prefixes := make([]string, len(keys))
	suffixes := make([]string, len(keys))
	for i, key := range keys {
		cut := strings.LastIndex(key, "+")
		prefixes[i], suffixes[i] = key[:cut+1], key[cut+1:]
	}
	for _, prefix := range prefixes {
		if prefix == "" || prefix != prefixes[0] {
			return strings.Join(keys, "/")
		}
	}
	return prefixes[0] + strings.Join(suffixes, "/")
}

// treeHelpLines mirrors upstream TreeHelp.render: " · "-joined help items
// packed into width-limited rows behind a two-space indent, in muted color.
func treeHelpLines(width int) []string {
	availableWidth := max(1, width)
	const indent, separator = "  ", " · "
	var lines []string
	flush := func(line string) {
		lines = append(lines, widthx.WrapTextWithAnsi(strings.TrimRight(line, " "), availableWidth)...)
	}
	start := func(item string) string {
		if widthx.VisibleWidth(indent+item) <= availableWidth {
			return indent + item
		}
		return item
	}
	current := ""
	for _, help := range treeHelpItems {
		item := help.label
		if keys := formatTreeHelpKeys(help.keys); keys != "" {
			item = keys + " " + help.label
			if help.labelFirst {
				item = help.label + " " + keys
			}
		}
		if current == "" {
			current = start(item)
			continue
		}
		if candidate := current + separator + item; widthx.VisibleWidth(candidate) <= availableWidth {
			current = candidate
			continue
		}
		flush(current)
		current = start(item)
	}
	if current != "" {
		flush(current)
	}
	for i, line := range lines {
		lines[i] = fg(ActiveTheme().Muted, line)
	}
	return lines
}
