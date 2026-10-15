package tui

// TreeSelectorComponent: single-pane navigable tree selector.
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
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// treeNodeWithLabelUpdate permits the selector to publish label edits before notifying its persistence callback.
type treeNodeWithLabelUpdate interface {
	SetNodeBranchLabel(label, timestamp string)
}

// TreeNode is the shape TreeSelectorComponent operates on. SessionTreeNode in
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

// TreeNodeWithCopyText exposes complete copyable entry content separately from its abbreviated display label.
type TreeNodeWithCopyText interface {
	NodeCopyText() *string
}

// TreeSelectorComponent overlay: a Container of spacer, borders, title, key hints, search line, the TreeList and the label input
// (tree-selector.ts:1336-1436). Done()/Cancelled()/SelectedID() mirror the FilterableList contract.
type TreeSelectorComponent struct {
	Container
	// TreeList is the list the component hosts; GetTreeList returns it (tree-selector.ts:1432).
	*TreeList
	treeContainer       *Container // holds the tree list child
	labelInputContainer *Container // holds the label input child
	Title               string
}

// TreeList is upstream's TreeList (tree-selector.ts:106-1335): the flattened session tree with its folding, filter, search,
// selection, scrolling and label-edit state, and the key handling that drives them.
type TreeList struct {
	// host is the Container that caches the rendered list; a change to the list invalidates it.
	host    *Container
	focused bool
	// OnSelect runs with the selected entry's ID on the confirm key (TreeList.onSelect).
	OnSelect func(entryID string)
	// OnCancel runs on the cancel key when no search is active (TreeList.onCancel).
	OnCancel func()
	// MaxVisibleLines is the number of tree rows shown at once. Upstream
	// TreeSelectorComponent uses max(5, floor(terminalHeight / 2)); see
	// TreeVisibleLines. Zero uses treeWindow.
	MaxVisibleLines int

	// Flattened DFS row-list. Split this into the
	// unconditional `allRows` (computed once at construction) and
	// the visible `rows` (re-derived from allRows whenever
	// foldedNodes changes). On a freshly-constructed picker the
	// two slices are identical until the user folds something.
	allRows        []treeRow
	root           TreeNode
	activePathIds  map[string]bool
	rows           []treeRow
	cursor         int
	lastSelectedId string
	scroll         int

	// foldedNodes contains the IDs of nodes whose descendants are
	// hidden via Ctrl+Left / Alt+Left fold. Recomputed-from-source
	// on every change. Mirrors upstream `foldedNodes` at
	// tree-selector.ts:65. Not persisted across /tree re-opens
	// (matches upstream behaviour: the picker is constructed fresh
	// each open so its state is per-session anyway).
	foldedNodes map[string]bool

	// foldable memoizes isFoldable per row ordinal (an index into allRows; false for a row that is not visible). recomputeVisible derives it
	// once whenever visibility changes so Render's per-row foldability check is O(1). Without it, isFoldable rebuilt O(n) maps and
	// visibleChildrenCount looped all rows calling it, making each Render O(n^2)+: the cause of /tree becoming unresponsive on large sessions.
	foldable []bool

	// labelCache / tagsCache memoize the per-node label and filter tags,
	// computed lazily on first use. Upstream's FlatNode stores only the
	// node and formats labels at render time (~20 visible rows); pig used
	// to format every node's markdown label AND unmarshal its message
	// twice during flatten, making /tree slow to open on large sessions.
	// Label saves invalidate the affected node's cached metadata.
	labelCache map[string]string
	tagsCache  map[string][]string
	// searchTexts memoizes each row's searchable lowercase text by row ordinal; searchHave marks the filled slots.
	searchTexts []string
	searchHave  []bool

	// The ordinal index over allRows (built by buildIndex): recomputeVisible works on slices indexed by a row's position in allRows instead of
	// maps keyed by entry id, and reuses these buffers between keystrokes. indexed is false until allRows is (re)built and indexed.
	indexed         bool
	idOrd           map[string]int32
	parentOrd       []int32 // ordinal of each row's parent in allRows, or -1 for a top-level row or one whose parent is not in the tree
	filterSkipCache map[string][]bool
	searchSkipBuf   []bool
	foldSkipBuf     []bool
	rowIdx          []int32 // index in rows of each allRows ordinal, or -1 when the row is not visible
	nearestOrd      []int32
	parentRowIdx    []int32
	childCount      []int32
	lastChild       []int32
	geometry        []rowGeometry // the gutters and prefix last built for each row ordinal, reused while the row's geometry does not change

	// showLabelTimestamps toggles inline `hh:mm` (or longer) display
	// of LabelEntry timestamps next to label-prefixed rows. Toggled
	// by `T` (Shift+T per upstream `app.tree.toggleLabelTimestamp`
	// at keybindings.ts:126-129). Defaults off.
	showLabelTimestamps bool

	// Configured app.tree.filter actions select or cycle the filter. Each open starts in default mode.
	filterMode  string
	filterModes []string

	// searchQuery is the active type-to-search string, matched as
	// lowercase whitespace-separated tokens against each node's
	// searchable text. Mirrors upstream `searchQuery`
	// (tree-selector.ts:113). Empty when no search is active.
	searchQuery string

	// The shared Input owns label editing; nil means the tree list is active.
	labelInput *TextInput
	labelNode  TreeNode
	// OnLabelEdit is called when the user commits a label (Enter).
	// entryID is the target entry; label is the new name (empty string
	// clears any existing label, matching upstream undefined→delete).
	// The caller is responsible for persisting via Session.AppendLabelChange.
	OnLabelEdit func(entryID, label string)
	// OnCopy receives the full selected entry text, or nil when the entry has no copyable text.
	OnCopy func(*string)

	done       bool
	cancelled  bool
	selectedID string
}

type treeRow struct {
	id     string
	ord    int32    // index of the row in TreeList.allRows; set by buildIndex
	prefix string   // ASCII tree connectors (gutters + connector cells)
	node   TreeNode // source node; label/tags computed lazily via TreeSelectorComponent caches
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

// rowGeometry is what a row's prefix and gutters were last built from, so an unchanged row keeps its strings across recomputes.
type rowGeometry struct {
	prefix        string
	gutters       []gutterInfo
	displayIndent int
	showConnector bool
	isLast        bool
	valid         bool
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
			line = row.gutter + widthx.SliceWithWidth(row.body, horizontalScroll, viewportWidth, true).Text + "\x1b[0m"
		}
		lines = append(lines, widthx.TruncateToWidth(line, width, "", false))
	}
	return lines
}

// NewTreeSelectorComponent builds the flattened view from the given root.
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

// cursorInRange reports whether the cursor selects a row; upstream reads filteredNodes[selectedIndex]?. for the same test.
func (t *TreeList) cursorInRange() bool { return t.cursor >= 0 && t.cursor < len(t.rows) }

func (t *TreeList) visibleLines() int {
	if t.MaxVisibleLines > 0 {
		return t.MaxVisibleLines
	}
	return treeWindow
}

// treeRoots is the virtual root the selector flattens when the host passes the session's root nodes, as the constructor's `tree` argument does.
type treeRoots []TreeNode

func (treeRoots) NodeID() string             { return "" }
func (treeRoots) NodeLabel() string          { return "" }
func (r treeRoots) NodeChildren() []TreeNode { return r }

// treeSelectorEmptyCancelDelay is the setTimeout delay after which an empty tree cancels itself (tree-selector.ts constructor).
const treeSelectorEmptyCancelDelay = 100 * time.Millisecond // upstream: packages/coding-agent/src/modes/interactive/components/tree-selector.ts:TreeSelectorComponent

// NewTreeSelectorComponentFrom builds the selector as tree-selector.ts's constructor does: tree holds the session's root nodes,
// currentLeafID the active leaf (nil when the session has none), terminalHeight sizes the window to max(5, floor(height/2)), and
// initialSelectedID (nil for none) and initialFilterMode (an unknown mode is "default") choose the opening state. onSelect and
// onCancel are the TreeList callbacks, run when the confirm or cancel key is handled; onLabelChange receives the committed label
// of a node, nil when it was cleared. An empty tree cancels itself after 100ms, so onCancel runs on a timer goroutine then.
// Any callback may be nil.
func NewTreeSelectorComponentFrom(tree []TreeNode, currentLeafID *string, terminalHeight int, onSelect func(entryID string), onCancel func(), onLabelChange func(entryID string, label *string), initialSelectedID *string, initialFilterMode string) *TreeSelectorComponent {
	t := newTreeSelect("Session Tree", treeRoots(tree), initialFilterMode, false)
	t.MaxVisibleLines = TreeVisibleLines(terminalHeight)
	leaf, selected := "", ""
	if currentLeafID != nil {
		leaf = *currentLeafID
	}
	if initialSelectedID != nil {
		selected = *initialSelectedID
	}
	t.SetInitialCursor(leaf, selected)
	list := t.GetTreeList()
	list.OnSelect, list.OnCancel = onSelect, onCancel
	if onLabelChange != nil {
		t.OnLabelEdit = func(entryID, label string) {
			if label == "" {
				onLabelChange(entryID, nil)
				return
			}
			onLabelChange(entryID, &label)
		}
	}
	if len(tree) == 0 && onCancel != nil {
		time.AfterFunc(treeSelectorEmptyCancelDelay, onCancel)
	}
	return t
}

// NewTreeSelectorComponent builds a selector in the default filter mode.
func NewTreeSelectorComponent(title string, root TreeNode) *TreeSelectorComponent {
	return NewTreeSelectWithInitialFilter(title, root, "default")
}

// NewTreeSelectWithInitialFilter builds a selector whose first visibility pass uses initialFilterMode, mirroring the TreeSelectorComponent initialFilterMode argument (tree-selector.ts constructor). An unknown mode falls back to "default".
func NewTreeSelectWithInitialFilter(title string, root TreeNode, initialFilterMode string) *TreeSelectorComponent {
	return newTreeSelect(title, root, initialFilterMode, true)
}

// newTreeSelect builds the selector; without flatten it leaves the rows to SetInitialCursor, which flattens the tree once the active path
// is known.
func newTreeSelect(title string, root TreeNode, initialFilterMode string, flatten bool) *TreeSelectorComponent {
	t := &TreeSelectorComponent{
		Title: title,
		TreeList: &TreeList{
			root:        root,
			foldedNodes: make(map[string]bool),
			// Keep the cycle order aligned with the filter-mode hint.
			filterMode:  "default",
			filterModes: []string{"default", "no-tools", "user-only", "labeled-only", "all"},
		},
	}
	t.host = &t.Container
	t.addChildren()
	if slices.Contains(t.filterModes, initialFilterMode) {
		t.filterMode = initialFilterMode
	}
	if root == nil || !flatten {
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

// SetInitialCursor orders and marks the active root-to-leaf path, then selects initialSelectedID or the current leaf. A hidden target resolves to its nearest visible ancestor. Call it after construction and before rendering.
func (t *TreeList) SetInitialCursor(currentLeafID, initialSelectedID string) {
	parents := make(map[string]string, len(t.allRows))
	for _, row := range t.allRows {
		parents[row.id] = row.parentID
	}
	if len(t.allRows) == 0 && t.root != nil {
		var walk func(parent string, nodes []TreeNode)
		walk = func(parent string, nodes []TreeNode) {
			for _, node := range nodes {
				id := node.NodeID()
				parents[id] = parent
				walk(id, node.NodeChildren())
			}
		}
		walk("", t.root.NodeChildren())
	}
	t.activePathIds = make(map[string]bool)
	for id := currentLeafID; id != ""; id = parents[id] {
		t.activePathIds[id] = true
	}
	if t.root != nil {
		t.allRows = t.allRows[:0]
		t.indexed = false
		roots := t.activeFirst(t.root.NodeChildren())
		multiple := len(roots) > 1
		indent := 0
		if multiple {
			indent = 1
		}
		for i, root := range roots {
			t.flatten(root, "", multiple, indent, multiple, i == len(roots)-1, multiple, multiple, multiple, nil, 0)
		}
		t.recomputeVisible()
	}
	target := initialSelectedID
	if target == "" {
		target = currentLeafID
	}
	// findNearestVisibleIndex falls back to the last visible row when neither the target nor any ancestor is visible.
	if idx, ok := t.nearestVisibleIndex(target); ok {
		t.cursor = idx
	} else {
		t.cursor = max(0, len(t.rows)-1)
	}
	if len(t.rows) > 0 {
		t.lastSelectedId = t.rows[t.cursor].id
	}
	t.fixScroll()
}

// activeFirst preserves sibling order except for moving the active branch first.
func (t *TreeList) activeFirst(nodes []TreeNode) []TreeNode {
	if len(nodes) < 2 {
		return nodes
	}
	for i, node := range nodes {
		if t.activePathIds[node.NodeID()] && i > 0 {
			ordered := slices.Clone(nodes)
			copy(ordered[1:i+1], nodes[:i])
			ordered[0] = node
			return ordered
		}
	}
	return nodes
}

// nearestVisibleIndex returns the index in the visible rows of entryID, or of
// its nearest visible ancestor when entryID is itself hidden. Mirrors upstream
// findNearestVisibleIndex (tree-selector.ts): walk up parent links until a
// visible row is found. Returns ok=false when nothing on the path is visible.
func (t *TreeList) nearestVisibleIndex(entryID string) (int, bool) {
	if entryID == "" || len(t.rows) == 0 {
		return 0, false
	}
	t.ensureIndex()
	cur, ok := t.idOrd[entryID]
	if !ok {
		return 0, false
	}
	for cur >= 0 {
		if idx := t.rowIdx[cur]; idx >= 0 {
			return int(idx), true
		}
		cur = t.parentOrd[cur]
	}
	return 0, false
}

// ensureIndex builds the ordinal index over allRows when it is stale.
func (t *TreeList) ensureIndex() {
	if !t.indexed {
		t.buildIndex()
	}
}

// buildIndex numbers the rows of allRows, resolves every parent to an ordinal and drops the caches that are keyed by ordinal. allRows is in
// depth-first order, so a row's parent precedes it. When two rows share an id the later one owns it, as a map keyed by id would.
func (t *TreeList) buildIndex() {
	n := len(t.allRows)
	t.idOrd = make(map[string]int32, n)
	for i := range t.allRows {
		t.allRows[i].ord = int32(i)
		t.idOrd[t.allRows[i].id] = int32(i)
	}
	t.parentOrd = slices.Grow(t.parentOrd[:0], n)[:n]
	for i := range t.allRows {
		parent := int32(-1)
		if id := t.allRows[i].parentID; id != "" {
			if ord, ok := t.idOrd[id]; ok {
				parent = ord
			}
		}
		t.parentOrd[i] = parent
	}
	t.filterSkipCache = nil
	t.geometry = nil
	t.searchTexts = make([]string, n)
	t.searchHave = make([]bool, n)
	t.indexed = true
}

// invalidateNodeCaches drops what is memoized about one node after its label changed.
func (t *TreeList) invalidateNodeCaches(id string) {
	delete(t.labelCache, id)
	delete(t.tagsCache, id)
	t.filterSkipCache = nil
	if ord, ok := t.idOrd[id]; ok && t.indexed {
		t.searchHave[ord] = false
	}
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
func (t *TreeList) flatten(n TreeNode, parentID string, parentMultipleChildren bool, indent int, showConnector, isLast, justBranched, isVirtualRootChild, multipleRoots bool, gutters []gutterInfo, depth int) {
	displayIndent := indent
	if multipleRoots {
		displayIndent = max(indent-1, 0)
	}

	kids := t.activeFirst(n.NodeChildren())
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

// buildTreePrefix renders the connector and gutter cells. Fold and active-path markers are composed by Render.
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

// filterSkipFor returns, by row ordinal, the rows the filter mode hides: usage entries such as cache-warming refreshes, rows the host tags
// "hidden" (assistant turns without text), and the rows the mode itself drops. It does not depend on the search query or the folds, so it is
// kept per mode until a label change or a rebuild of the rows.
// Mirrors upstream tree-selector.ts applyFilter (:303-319).
func (t *TreeList) filterSkipFor(mode string) []bool {
	if skip, ok := t.filterSkipCache[mode]; ok {
		return skip
	}
	skip := make([]bool, len(t.allRows))
	for i, r := range t.allRows {
		tags := t.rowTags(r)
		hidden := slices.Contains(tags, "usage") || slices.Contains(tags, "hidden")
		switch mode {
		case "default":
			hidden = hidden || slices.Contains(tags, "settings")
		case "no-tools":
			// Default minus tool-result rows (:314-316).
			hidden = hidden || slices.Contains(tags, "settings") || slices.Contains(tags, "tool_result")
		case "user-only":
			// Only user-message rows (:308-310).
			hidden = hidden || !slices.Contains(tags, "user")
		case "labeled-only":
			// Only rows that carry a user-set label (:317-319).
			hidden = hidden || !slices.Contains(tags, "labeled")
		}
		// "all": only the rows every mode hides.
		skip[i] = hidden
	}
	if t.filterSkipCache == nil {
		t.filterSkipCache = make(map[string][]bool)
	}
	t.filterSkipCache[mode] = skip
	return skip
}

// recomputeVisible rebuilds the visible geometry from the complete tree. It preserves the selected entry through empty results and otherwise walks to its nearest visible ancestor, falling back to the last row.
//
// It works on slices indexed by a row's ordinal in allRows and reuses their buffers, so a keystroke allocates only the changed row strings: the
// filter-mode skip set is cached per mode, the search skip set is computed from the memoized lowercase texts, and the fold skip set, visible
// rows, nearest visible ancestors and child counts are derived in one pass each.
func (t *TreeList) recomputeVisible() {
	t.ensureIndex()
	if t.cursorInRange() {
		t.lastSelectedId = t.rows[t.cursor].id
	}
	n := len(t.allRows)
	// Two-pass filter (matches upstream tree-selector.ts:282-352 pre-recomputeVisualStructure): the filter-mode skip, then the fold skip: rows
	// whose ancestor is folded are dropped. The filter-mode skip is kept apart because folded nodes may themselves be filter-hidden, in which
	// case their descendants are still hidden by the fold transitively (mirrors upstream `skipSet` logic at :349-352).
	filterSkip := t.filterSkipFor(t.filterMode)

	// Type-to-search filter, mirroring upstream tree-selector.ts:337 and :390-393. Tokens are the lowercase query split on whitespace; a row
	// survives only if every token is a substring of its searchable text; lowercasing and whitespace follow JavaScript's toLowerCase and \s.
	// Applying it here keeps recomputeVisible the single place filter/fold/search visibility is re-derived.
	var searchSkip []bool
	if t.searchQuery != "" {
		if tokens := strings.FieldsFunc(jsstring.ToLower(t.searchQuery), jsstring.IsSpace); len(tokens) > 0 {
			t.searchSkipBuf = resizeBools(t.searchSkipBuf, n)
			searchSkip = t.searchSkipBuf
			for i := range t.allRows {
				text := t.rowSearchText(t.allRows[i])
				searchSkip[i] = false
				for _, tok := range tokens {
					if !strings.Contains(text, tok) {
						searchSkip[i] = true
						break
					}
				}
			}
		}
	}

	t.foldSkipBuf = resizeBools(t.foldSkipBuf, n)
	foldSkip := t.foldSkipBuf
	anyFolded := len(t.foldedNodes) > 0
	t.rows = t.rows[:0]
	for i := range t.allRows {
		r := &t.allRows[i]
		foldSkip[i] = false
		if r.parentID != "" {
			if p := t.parentOrd[i]; (anyFolded && t.foldedNodes[r.parentID]) || (p >= 0 && foldSkip[p]) {
				foldSkip[i] = true
				continue
			}
		}
		if filterSkip[i] || (searchSkip != nil && searchSkip[i]) {
			continue
		}
		t.rows = append(t.rows, *r)
	}
	t.rowIdx = resizeInt32s(t.rowIdx, n)
	for i := range t.rowIdx {
		t.rowIdx[i] = -1
	}
	for i := range t.rows {
		t.rowIdx[t.rows[i].ord] = int32(i)
	}
	if t.lastSelectedId != "" {
		if index, ok := t.nearestVisibleIndex(t.lastSelectedId); ok {
			t.cursor = index
		} else {
			t.cursor = max(0, len(t.rows)-1)
		}
	} else if t.cursor >= len(t.rows) {
		t.cursor = max(0, len(t.rows)-1)
	}
	if t.cursorInRange() {
		t.lastSelectedId = t.rows[t.cursor].id
	}
	t.recalculateVisualStructure()
	t.fixScroll()
}

// resizeBools returns a slice of n zero values, reusing buf's array when it is large enough.
func resizeBools(buf []bool, n int) []bool {
	if cap(buf) < n {
		return make([]bool, n)
	}
	buf = buf[:n]
	clear(buf)
	return buf
}

// resizeInt32s returns a slice of length n over buf's array when it is large enough; the caller sets every element.
func resizeInt32s(buf []int32, n int) []int32 {
	if cap(buf) < n {
		return make([]int32, n)
	}
	return buf[:n]
}

// recalculateVisualStructure attaches filtered descendants to their nearest visible ancestor before deriving connectors, and the foldable rows
// (tree-selector.ts:1010-1018: a row is foldable iff it has visible children and either no visible parent or a visible parent with several
// children). The depth-first order of allRows makes the ancestor lookup linear even through long hidden chains.
func (t *TreeList) recalculateVisualStructure() {
	n, rows := len(t.allRows), len(t.rows)
	t.nearestOrd = resizeInt32s(t.nearestOrd, n)
	t.parentRowIdx = resizeInt32s(t.parentRowIdx, rows)
	// childCount and lastChild hold one slot per visible row and a last slot for the virtual root.
	t.childCount = resizeInt32s(t.childCount, rows+1)
	t.lastChild = resizeInt32s(t.lastChild, rows+1)
	clear(t.childCount)
	for i := range t.lastChild {
		t.lastChild[i] = -1
	}
	rootSlot := int32(rows)
	for i := range t.allRows {
		parent := int32(-1)
		if p := t.parentOrd[i]; p >= 0 {
			parent = t.nearestOrd[p]
		}
		ri := t.rowIdx[i]
		if ri < 0 {
			t.nearestOrd[i] = parent
			continue
		}
		slot := rootSlot
		if parent >= 0 {
			t.rows[ri].parentID = t.allRows[parent].id
			slot = t.rowIdx[parent]
		} else {
			t.rows[ri].parentID = ""
		}
		t.parentRowIdx[ri] = slot
		t.childCount[slot]++
		t.lastChild[slot] = ri
		t.nearestOrd[i] = int32(i)
	}
	multipleRoots := t.childCount[rootSlot] > 1
	t.foldable = resizeBools(t.foldable, n)
	if len(t.geometry) != n {
		t.geometry = make([]rowGeometry, n)
	}
	for i := range t.rows {
		row := &t.rows[i]
		slot := t.parentRowIdx[i]
		row.nChildren = int(t.childCount[i])
		row.parentMultipleChildren = t.childCount[slot] > 1
		row.isLast = t.lastChild[slot] == int32(i)
		row.isVirtualRootChild = slot == rootSlot && multipleRoots
		row.showConnector = row.parentMultipleChildren && slot != rootSlot
		indent := 0
		row.gutters = nil
		if slot == rootSlot {
			if multipleRoots {
				indent = 1
			}
		} else {
			parent := &t.rows[slot]
			indent = parent.depth
			if parent.nChildren > 1 || (parent.parentMultipleChildren && indent > 0) {
				indent++
			}
			row.gutters = parent.gutters
			if parent.showConnector {
				// The gutters are the parent's plus one column; the previous build's slice is reused while the parent column it was made from is unchanged.
				extra := gutterInfo{position: max(parent.displayIndent-1, 0), show: !parent.isLast}
				if prev := &t.geometry[row.ord]; prev.valid && len(prev.gutters) == len(parent.gutters)+1 && slices.Equal(prev.gutters[:len(parent.gutters)], parent.gutters) && prev.gutters[len(parent.gutters)] == extra {
					row.gutters = prev.gutters
				} else {
					row.gutters = append(slices.Clone(parent.gutters), extra)
				}
			}
		}
		row.depth = indent
		row.displayIndent = indent
		if multipleRoots {
			row.displayIndent = max(0, indent-1)
		}
		if prev := &t.geometry[row.ord]; prev.valid && prev.displayIndent == row.displayIndent && prev.showConnector == row.showConnector && prev.isLast == row.isLast && slices.Equal(prev.gutters, row.gutters) {
			row.prefix = prev.prefix
		} else {
			row.prefix = buildTreePrefix(row.displayIndent, row.showConnector, row.isLast, row.gutters)
			*prev = rowGeometry{prefix: row.prefix, displayIndent: row.displayIndent, showConnector: row.showConnector, isLast: row.isLast, gutters: row.gutters, valid: true}
		}
		t.geometry[row.ord].gutters = row.gutters
		t.foldable[row.ord] = row.nChildren > 0 && (slot == rootSlot || t.childCount[slot] > 1)
	}
}

// rowLabel returns the node's rendered label, computing it lazily and
// memoizing per node id. Mirrors upstream's render-time label formatting
// (only visible rows are ever formatted in practice).
func (t *TreeList) rowLabel(r treeRow) string {
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
func (t *TreeList) rowTags(r treeRow) []string {
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

// WarmSearchTexts memoizes every row's lowercase searchable text, so the first search keystroke is a plain scan instead of building the texts.
// The host calls it once after the selector's first paint, before it reads input.
func (t *TreeList) WarmSearchTexts() {
	t.ensureIndex()
	for i := range t.allRows {
		t.rowSearchText(t.allRows[i])
	}
}

// rowSearchText returns the node's lowercase searchable text, memoized.
// Mirrors the searchable-text sourcing in upstream getSearchableText
// (tree-selector.ts:559-600) via the TreeNodeSearchableText adapter.
// Only touched when a search query is active.
func (t *TreeList) rowSearchText(r treeRow) string {
	t.ensureIndex()
	if t.searchHave[r.ord] {
		return t.searchTexts[r.ord]
	}
	// A node with searchable text is matched on it alone, so its display label is not formatted for the search.
	var v string
	if st, ok := r.node.(TreeNodeSearchableText); ok {
		v = jsstring.ToLower(st.NodeSearchableText())
	} else {
		v = jsstring.ToLower(r.node.NodeLabel())
	}
	t.searchTexts[r.ord], t.searchHave[r.ord] = v, true
	return v
}

// isFoldable mirrors upstream tree-selector.ts:1010-1018: a node is
// foldable iff it has visible children AND either has no visible
// parent (top-level row / virtual-root child) or its visible parent
// has multiple visible children (segment start under a branch point).
// Reads the foldableCache built by computeFoldable.
func (t *TreeList) isFoldable(row treeRow) bool {
	return int(row.ord) < len(t.foldable) && t.foldable[row.ord]
}

// formatLabelTimestamp mirrors upstream tree-selector.ts:862-885 (formatLabelTimestamp).
// Same-day  → `hh:mm`
// Same-year → `M/D hh:mm`
// Other     → `YY/M/D hh:mm`
// The timestamp is read by Date.parse; one it does not recognise gives Pi's text for an invalid Date ("aN/NaN/NaN NaN:NaN"). An empty
// timestamp gives "", as the callers' truthiness test does upstream.
func formatLabelTimestamp(ts string, now time.Time) string {
	if ts == "" {
		return ""
	}
	ms := ai.DateParseIn(ts, now.Location())
	if math.IsNaN(ms) {
		return "aN/NaN/NaN NaN:NaN"
	}
	t := time.UnixMilli(int64(ms)).In(now.Location())
	hhmm := fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return hhmm
	}
	if t.Year() == now.Year() {
		return fmt.Sprintf("%d/%d %s", int(t.Month()), t.Day(), hhmm)
	}
	// getFullYear().toString().slice(-2): the last two characters, so a year below 10 or above -10 keeps its one digit (or its sign).
	year := strconv.Itoa(t.Year())
	if len(year) > 2 {
		year = year[len(year)-2:]
	}
	return fmt.Sprintf("%s/%d/%d %s", year, int(t.Month()), t.Day(), hhmm)
}

// Done / Cancelled / SelectedID implement the modal contract.
// GetTreeList returns the list the component hosts (tree-selector.ts:1432-1434).
func (t *TreeSelectorComponent) GetTreeList() *TreeList { return t.TreeList }

// Render draws the component's children. TreeList also has a Render, so the component names its own.
func (t *TreeSelectorComponent) Render(width int) []string { return t.Container.Render(width) }

// Invalidate drops the Container's cached lines.
func (t *TreeSelectorComponent) Invalidate() { t.Container.Invalidate() }

// Invalidate tells the hosting Container that the list changed.
func (t *TreeList) Invalidate() {
	if t.host != nil {
		t.host.Invalidate()
	}
}

// Render draws the visible tree rows and the position line (tree-selector.ts TreeList.render).
func (t *TreeList) Render(width int) []string { return t.renderTreeList(width) }

// GetSelectedNode returns the node under the cursor, or nil when no row is visible (TreeList.getSelectedNode).
func (t *TreeList) GetSelectedNode() TreeNode {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return nil
	}
	return t.rows[t.cursor].node
}

func (t *TreeList) Done() bool         { return t.done }
func (t *TreeList) Cancelled() bool    { return t.cancelled }
func (t *TreeList) SelectedID() string { return t.selectedID }

func (t *TreeList) getStatusLabels() string {
	labels := ""
	switch t.filterMode {
	case "no-tools":
		labels = " [no-tools]"
	case "user-only":
		labels = " [user]"
	case "labeled-only":
		labels = " [labeled]"
	case "all":
		labels = " [all]"
	}
	if t.showLabelTimestamps {
		labels += " [+label time]"
	}
	return labels
}

// addChildren adds Pi's constructor children (tree-selector.ts:1379-1393). The tree list and the label input are private children that render only while their mode is active, so a label edit swaps what the two containers show.
func (t *TreeSelectorComponent) addChildren() {
	t.treeContainer = NewContainer()
	t.treeContainer.Add(treeSection(t.renderTreeList))
	t.labelInputContainer = NewContainer()
	t.labelInputContainer.Add(treeSection(t.renderLabelInput))
	t.Add(NewSpacer(1))
	t.Add(NewDynamicBorder())
	t.Add(NewPaddedText(ActiveTheme().Bold("  Session Tree"), 1, 0, nil))
	t.Add(treeSection(t.renderHelp))
	t.Add(treeSection(t.renderSearchLine))
	t.Add(NewDynamicBorder())
	t.Add(NewSpacer(1))
	t.Add(t.treeContainer)
	t.Add(t.labelInputContainer)
	t.Add(NewSpacer(1))
	t.Add(NewDynamicBorder())
}

// treeSection renders one section from the selector's current state on every frame, so a parent Container never serves a stale one.
type treeSection func(width int) []string

func (s treeSection) Render(width int) []string { return s(max(width, 0)) }
func (treeSection) Invalidate()                 {}

// renderHelp draws the key hint rows; the header stays visible while label input replaces the tree rows (TreeHelp).
func (t *TreeSelectorComponent) renderHelp(width int) []string {
	headerHint := treeHelpLines(width)
	lines := make([]string, 0, len(headerHint))
	for _, line := range headerHint {
		lines = append(lines, widthx.TruncateToWidth(line, width, "...", false))
	}
	return lines
}

// renderSearchLine draws "Type to search:" in muted, mirroring tree-selector.ts:1169-1171; the active query renders in accent after it (SearchLine).
func (t *TreeSelectorComponent) renderSearchLine(width int) []string {
	searchLine := "  " + fg(ActiveTheme().Muted, "Type to search:")
	if t.searchQuery != "" {
		searchLine += " " + fg(ActiveTheme().Accent, t.searchQuery)
	}
	return []string{widthx.TruncateToWidth(searchLine, width, "...", false)}
}

// renderLabelInput draws the label prompt, the shared Input and the configured key hints while a label is edited (LabelInput).
func (t *TreeSelectorComponent) renderLabelInput(width int) []string {
	if t.labelInput == nil {
		return nil
	}
	lines := []string{widthx.TruncateToWidth("  "+fg(ActiveTheme().Muted, "Label (empty to remove):"), width, "...", false)}
	for _, line := range t.labelInput.Render(width - 2) {
		lines = append(lines, widthx.TruncateToWidth("  "+line, width, "...", false))
	}
	return append(lines, widthx.TruncateToWidth("  "+RawKeyHint(strings.Join(GetTUIKeybindings().GetKeys(KBSelectConfirm), "/"), "save")+"  "+RawKeyHint(strings.Join(GetTUIKeybindings().GetKeys(KBSelectCancel), "/"), "cancel"), width, "...", false))
}

// renderTreeList draws the visible tree rows and the position line, or the empty-state rows (TreeList). Rows are capped to the scroll window used by fixScroll so the tree cannot overflow the editor slot.
func (t *TreeList) renderTreeList(width int) []string {
	if t.labelInput != nil {
		return nil
	}
	var lines []string
	if len(t.rows) == 0 {
		lines = append(lines, widthx.TruncateToWidth(fg(ActiveTheme().Muted, "  No entries found"), width, "...", false), widthx.TruncateToWidth(fg(ActiveTheme().Muted, "  (0/0)"+t.getStatusLabels()), width, "...", false))
		return lines
	}
	//portlint:allow clock formatLabelTimestamp's `const now = new Date()` (tree-selector.ts:864); the TUI.now seam times renders
	now := time.Now()
	maxRows := min(t.visibleLines(), len(t.rows))
	viewportRows := make([]horizontalViewportRow, 0, maxRows)
	for i := range maxRows {
		idx := t.scroll + i
		if idx >= len(t.rows) {
			break
		}
		r := t.rows[idx]
		// Cursor, tree geometry, active-path marker, then entry content.
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
			foldMarker = fg(ActiveTheme().Accent, "⊞ ")
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
		labelTimestamp := r.labelTimestamp
		if node, ok := r.node.(TreeNodeWithLabelTimestamp); ok {
			labelTimestamp = node.NodeLabelTimestamp()
		}
		labelTs := ""
		if t.showLabelTimestamps && labelTimestamp != "" {
			if s := formatLabelTimestamp(labelTimestamp, now); s != "" {
				labelTs = fg(ActiveTheme().Muted, s+" ")
			}
		}

		// Path-marker bullet is accent-coloured, mirroring upstream's
		// `theme.fg("accent", "• ")` pathMarker. Selected-row content is
		// bolded (upstream `isSelected ? theme.bold(result)`), closed with
		// the scoped intensity reset (SGR 22) so the selected-row background
		// survives.
		pathMarker := ""
		if t.activePathIds[r.id] {
			pathMarker = fg(ActiveTheme().Accent, "• ")
		}
		prefixPart := fg(ActiveTheme().Dim, prefix) + foldMarker + pathMarker
		label := t.rowLabel(r)
		if isSelected {
			label = ActiveTheme().Bold(label)
		}
		body := prefixPart + branchLabel + labelTs + label
		gutter := cursor
		if isSelected {
			gutter, body = ActiveTheme().Bg("selectedBg", gutter), ActiveTheme().Bg("selectedBg", body)
		}
		viewportRows = append(viewportRows, horizontalViewportRow{
			gutter:     gutter,
			body:       body,
			anchorCol:  treeVisibleWidth(prefixPart),
			bodyWidth:  treeVisibleWidth(body),
			isSelected: isSelected,
		})
	}
	lines = append(lines, renderTreeHorizontalViewport(viewportRows, width)...)
	lines = append(lines, widthx.TruncateToWidth(fg(ActiveTheme().Muted, fmt.Sprintf("  (%d/%d)%s", t.cursor+1, len(t.rows), t.getStatusLabels())), width, "...", false))
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
func (t *TreeList) findBranchSegmentStart(direction string) int {
	if !t.cursorInRange() {
		return t.cursor
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

// CapturesSelectPageInput reports that the list handles tui.select.pageUp and tui.select.pageDown.
func (t *TreeList) CapturesSelectPageInput() bool { return true }

// HandleInput moves the cursor, commits the selection, or delegates label editing to the shared Input.
//
// Ctrl+Left/Alt+Left folds the highlighted branch or moves to the current
// branch segment start. Ctrl+Right/Alt+Right unfolds it or moves to the next
// branch segment. Shift+T toggles label timestamps. Colliding bindings use upstream's action priority.
func (t *TreeList) HandleInput(data string) {
	// When in label-edit mode, intercept all keystrokes
	// for the inline editor instead of the normal navigation handlers.
	if t.labelInput != nil {
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
	// The cursor is not confined to the rows: like upstream's selectedIndex it goes to -1 or past the end when a key moves it over an empty list, and the next filter change keeps it there while no row has been selected yet.
	case kb.Matches(data, KBSelectUp):
		if t.cursor == 0 {
			t.cursor = len(t.rows) - 1
		} else {
			t.cursor--
		}
		t.fixScroll()
	case kb.Matches(data, KBSelectDown):
		if t.cursor == len(t.rows)-1 {
			t.cursor = 0
		} else {
			t.cursor++
		}
		t.fixScroll()
	case kb.Matches(data, "app.tree.foldOrUp"):
		if t.cursorInRange() && t.isFoldable(t.rows[t.cursor]) && !t.foldedNodes[t.rows[t.cursor].id] {
			cur := t.rows[t.cursor]
			t.foldedNodes[cur.id] = true
			t.recomputeVisible()
		} else {
			t.cursor = t.findBranchSegmentStart("up")
			t.fixScroll()
		}
	case kb.Matches(data, "app.tree.unfoldOrDown"):
		if t.cursorInRange() && t.foldedNodes[t.rows[t.cursor].id] {
			delete(t.foldedNodes, t.rows[t.cursor].id)
			t.recomputeVisible()
		} else {
			t.cursor = t.findBranchSegmentStart("down")
			t.fixScroll()
		}
	case kb.Matches(data, KBEditorCursorLeft) || kb.Matches(data, KBSelectPageUp):
		t.cursor = max(0, t.cursor-t.visibleLines())
		t.fixScroll()
	case kb.Matches(data, KBEditorCursorRight) || kb.Matches(data, KBSelectPageDown):
		t.cursor = min(len(t.rows)-1, t.cursor+t.visibleLines())
		t.fixScroll()
	case kb.Matches(data, KBSelectConfirm):
		if t.cursorInRange() {
			t.selectedID = t.rows[t.cursor].id
			t.done = true
			if t.OnSelect != nil {
				t.OnSelect(t.selectedID)
			}
		}
	case kb.Matches(data, "app.message.copy"):
		if t.OnCopy != nil {
			var text *string
			if t.cursor >= 0 && t.cursor < len(t.rows) {
				if node, ok := t.rows[t.cursor].node.(TreeNodeWithCopyText); ok {
					text = node.NodeCopyText()
				}
			}
			t.OnCopy(text)
		}
	case kb.Matches(data, KBSelectCancel):
		// Esc with an active search clears the query instead of closing the picker.
		if t.searchQuery != "" {
			t.searchQuery = ""
			clear(t.foldedNodes)
			t.recomputeVisible()
			break
		}
		t.cancelled = true
		t.done = true
		if t.OnCancel != nil {
			t.OnCancel()
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
	case kb.Matches(data, KBEditorDeleteCharBack):
		if t.searchQuery != "" {
			t.searchQuery = jsstring.Slice(t.searchQuery, 0, -1)
			t.foldedNodes = map[string]bool{}
			t.recomputeVisible()
		}
	case kb.Matches(data, "app.tree.editLabel"):
		if len(t.rows) > 0 && t.cursor >= 0 && t.cursor < len(t.rows) {
			t.labelNode = t.rows[t.cursor].node
			t.labelInput = NewInput(InputOptions{})
			t.labelInput.SetFocused(t.focused)
			if node, ok := t.labelNode.(TreeNodeWithBranchLabel); ok {
				t.labelInput.SetValue(node.NodeBranchLabel())
			}
		}
	case kb.Matches(data, "app.tree.toggleLabelTimestamp"):
		t.showLabelTimestamps = !t.showLabelTimestamps
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

// handleLabelInput saves or cancels the label; all editing operations belong to the shared Input.
func (t *TreeList) handleLabelInput(data string) {
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectConfirm):
		label := widthx.JSTrim(t.labelInput.GetValue())
		timestamp := ""
		if label != "" {
			//portlint:allow clock updateNodeLabel's `new Date().toISOString()` (tree-selector.ts:641)
			timestamp = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if node, ok := t.labelNode.(treeNodeWithLabelUpdate); ok {
			node.SetNodeBranchLabel(label, timestamp)
		}
		id := t.labelNode.NodeID()
		t.invalidateNodeCaches(id)
		if t.OnLabelEdit != nil {
			t.OnLabelEdit(id, label)
		}
		t.labelInput, t.labelNode = nil, nil
	case kb.Matches(data, KBSelectCancel):
		t.labelInput, t.labelNode = nil, nil
	default:
		t.labelInput.HandleInput(data)
	}
}

// cycleFilterMode cycles the filter, clears folds, and preserves selection through the shared visibility rebuild.
func (t *TreeList) cycleFilterMode(direction int) {
	if len(t.filterModes) <= 1 {
		return
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
	clear(t.foldedNodes)
	t.recomputeVisible()
}

// setFilterMode toggles a non-default filter back to default when selected again. Every direct filter action clears folds, including reselecting default.
func (t *TreeList) setFilterMode(mode string) {
	if t.filterMode == mode {
		mode = "default"
	}
	t.filterMode = mode
	clear(t.foldedNodes)
	t.recomputeVisible()
}

// NativeNode reports the visible rows as a selector whose item ids are the
// node ids, checked on the active path, with the filter modes as tabs in the
// order app.tree.filter.cycleForward steps through them. pig additive (D91):
// a row's label is its plain text after the tree connectors (fold marker,
// branch label, label time, entry), the status the filter and label time
// labels, and the label editor is not modelled, so the dock shows it as
// lines.
func (t *TreeSelectorComponent) NativeNode() (frontend.Node, bool) {
	if t.labelInput != nil {
		return nil, false
	}
	node := frontend.Selector{
		Title:      "Session Tree",
		Searchable: true,
		Query:      t.searchQuery,
		Items:      make([]frontend.SelectorItem, len(t.rows)),
		Tabs:       make([]frontend.SelectorTab, len(t.filterModes)),
		Tab:        t.filterMode,
		Status:     strings.TrimSpace(t.getStatusLabels()),
	}
	for i, mode := range t.filterModes {
		node.Tabs[i] = frontend.SelectorTab{ID: mode, Label: mode}
	}
	//portlint:allow clock formatLabelTimestamp's `const now = new Date()` (tree-selector.ts:864) for the frontend node's label times
	now := time.Now()
	for i, r := range t.rows {
		var label strings.Builder
		if t.foldedNodes[r.id] {
			label.WriteString("⊞ ")
		}
		if bn, ok := r.node.(TreeNodeWithBranchLabel); ok {
			if s := bn.NodeBranchLabel(); s != "" {
				label.WriteString("[" + s + "] ")
			}
		}
		if t.showLabelTimestamps {
			labelTimestamp := r.labelTimestamp
			if node, ok := r.node.(TreeNodeWithLabelTimestamp); ok {
				labelTimestamp = node.NodeLabelTimestamp()
			}
			if s := formatLabelTimestamp(labelTimestamp, now); s != "" {
				label.WriteString(s + " ")
			}
		}
		label.WriteString(t.rowLabel(r))
		node.Items[i] = frontend.SelectorItem{ID: r.id, Label: plainText(label.String()), Checked: t.activePathIds[r.id], Depth: r.displayIndent}
	}
	if t.cursor >= 0 && t.cursor < len(node.Items) {
		node.Selected = node.Items[t.cursor].ID
	}
	return node, true
}

// nativeTabKey reports the key that steps the filter modes.
func (t *TreeSelectorComponent) nativeTabKey() TUIKeybinding { return "app.tree.filter.cycleForward" }

// fixScroll keeps the cursor inside the visible window. Mirrors upstream
// tree-selector.ts:611-619 which centers the selection within
// maxVisibleLines and clamps to valid range.
func (t *TreeList) fixScroll() {
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
	pattern *lazyregexp.Regexp
	text    string
}{
	{lazyregexp.New(`\bpageUp\b`), "pgup"},
	{lazyregexp.New(`\bpageDown\b`), "pgdn"},
	{lazyregexp.New(`\bup\b`), "↑"},
	{lazyregexp.New(`\bdown\b`), "↓"},
	{lazyregexp.New(`\bleft\b`), "←"},
	{lazyregexp.New(`\bright\b`), "→"},
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

// SetFocused records TUI focus and propagates it to an active label input for IME cursor positioning (upstream `set focused`).
func (t *TreeSelectorComponent) SetFocused(focused bool) {
	t.mu.Lock() // a render holds the container's read lock while it draws the label input
	defer t.mu.Unlock()
	t.focused = focused
	if t.labelInput != nil {
		t.labelInput.SetFocused(focused)
	}
}

// Focused reports the TUI focus flag (upstream `get focused`).
func (t *TreeSelectorComponent) Focused() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.focused
}
