package tui

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// recomputeNode is a TreeNode with filter tags and searchable text, the shape of the session adapter's nodes.
type recomputeNode struct {
	id     string
	label  string
	search string
	tags   []string
	kids   []TreeNode
}

func (n *recomputeNode) NodeID() string             { return n.id }
func (n *recomputeNode) NodeLabel() string          { return n.label }
func (n *recomputeNode) NodeChildren() []TreeNode   { return n.kids }
func (n *recomputeNode) NodeFilterTags() []string   { return n.tags }
func (n *recomputeNode) NodeSearchableText() string { return n.search }

var recomputeWords = []string{"error", "test", "commit", "Parser", "deploy", "ÉCOLE", "İstanbul", "alpha", "beta", "gamma"}
var recomputeTagSets = [][]string{nil, nil, nil, {"user"}, {"tool_result"}, {"settings"}, {"usage"}, {"hidden"}, {"labeled"}, {"user", "labeled"}, {"settings", "tool_result"}}

// randomRecomputeTree builds a forest of n nodes with branch points, chains, mixed tags and searchable text. Several top-level roots exercise the
// virtual-root indent rule, and every node id is unique.
func randomRecomputeTree(rng *rand.Rand, n int) TreeNode {
	root, _ := randomRecomputeForest(rng, n)
	return root
}

// randomRecomputeForest also returns the generated nodes in creation order.
func randomRecomputeForest(rng *rand.Rand, n int) (TreeNode, []*recomputeNode) {
	nodes := make([]*recomputeNode, 0, n)
	roots := &recomputeNode{id: ""}
	for i := range n {
		node := &recomputeNode{id: fmt.Sprintf("n%d", i), tags: recomputeTagSets[rng.IntN(len(recomputeTagSets))]}
		words := []string{recomputeWords[rng.IntN(len(recomputeWords))], recomputeWords[rng.IntN(len(recomputeWords))]}
		node.label = strings.Join(words, " ")
		node.search = strings.Join(append(words, fmt.Sprintf("entry %d", i)), " ")
		if len(nodes) == 0 || rng.IntN(25) == 0 {
			roots.kids = append(roots.kids, node)
		} else {
			// Prefer recent nodes so chains form, sometimes a branch point from an older one.
			var parent *recomputeNode
			if rng.IntN(4) == 0 {
				parent = nodes[rng.IntN(len(nodes))]
			} else {
				parent = nodes[max(0, len(nodes)-1-rng.IntN(3))]
			}
			parent.kids = append(parent.kids, node)
		}
		nodes = append(nodes, node)
	}
	return roots, nodes
}

// referenceRecompute is the string-keyed recomputeVisible that preceded the ordinal-slice one: the same rules over maps keyed by entry id. It
// reads the list without changing it and returns the state recomputeVisible leaves.
type referenceState struct {
	rows           []treeRow
	cursor         int
	lastSelectedID string
	foldable       map[string]bool
}

func referenceRecompute(t *TreeList) referenceState {
	rows := slices.Clone(t.rows)
	cursor, lastSelected := t.cursor, t.lastSelectedId
	inRange := func() bool { return cursor >= 0 && cursor < len(rows) }
	if inRange() {
		lastSelected = rows[cursor].id
	}
	tagsOf := func(r treeRow) []string {
		if ft, ok := r.node.(TreeNodeWithFilterTags); ok {
			return ft.NodeFilterTags()
		}
		return nil
	}
	searchTextOf := func(r treeRow) string {
		if st, ok := r.node.(TreeNodeSearchableText); ok {
			return jsstring.ToLower(st.NodeSearchableText())
		}
		return jsstring.ToLower(r.node.NodeLabel())
	}
	filterSkip := map[string]bool{}
	for _, r := range t.allRows {
		if tags := tagsOf(r); slices.Contains(tags, "usage") || slices.Contains(tags, "hidden") {
			filterSkip[r.id] = true
		}
	}
	for _, r := range t.allRows {
		tags := tagsOf(r)
		switch t.filterMode {
		case "default":
			if slices.Contains(tags, "settings") {
				filterSkip[r.id] = true
			}
		case "no-tools":
			if slices.Contains(tags, "settings") || slices.Contains(tags, "tool_result") {
				filterSkip[r.id] = true
			}
		case "user-only":
			if !slices.Contains(tags, "user") {
				filterSkip[r.id] = true
			}
		case "labeled-only":
			if !slices.Contains(tags, "labeled") {
				filterSkip[r.id] = true
			}
		}
	}
	searchSkip := map[string]bool{}
	if t.searchQuery != "" {
		if tokens := strings.FieldsFunc(jsstring.ToLower(t.searchQuery), jsstring.IsSpace); len(tokens) > 0 {
			for _, r := range t.allRows {
				text := searchTextOf(r)
				for _, tok := range tokens {
					if !strings.Contains(text, tok) {
						searchSkip[r.id] = true
						break
					}
				}
			}
		}
	}
	rows = rows[:0]
	foldSkip := map[string]bool{}
	for _, r := range t.allRows {
		if r.parentID != "" && (t.foldedNodes[r.parentID] || foldSkip[r.parentID]) {
			foldSkip[r.id] = true
			continue
		}
		if filterSkip[r.id] || searchSkip[r.id] {
			continue
		}
		rows = append(rows, r)
	}
	nearestIndex := func(entryID string) (int, bool) {
		if entryID == "" || len(rows) == 0 {
			return 0, false
		}
		parentByID := map[string]string{}
		for _, r := range t.allRows {
			parentByID[r.id] = r.parentID
		}
		visibleIdx := map[string]int{}
		for i, r := range rows {
			visibleIdx[r.id] = i
		}
		for cur := entryID; cur != ""; {
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
	if lastSelected != "" {
		if index, ok := nearestIndex(lastSelected); ok {
			cursor = index
		} else {
			cursor = max(0, len(rows)-1)
		}
	} else if cursor >= len(rows) {
		cursor = max(0, len(rows)-1)
	}
	if inRange() {
		lastSelected = rows[cursor].id
	}
	// recalculateVisualStructure
	indices := map[string]int{}
	for i, row := range rows {
		indices[row.id] = i
	}
	nearest := map[string]string{}
	children := map[string][]int{}
	for _, row := range t.allRows {
		parent := nearest[row.parentID]
		if i, visible := indices[row.id]; visible {
			rows[i].parentID = parent
			children[parent] = append(children[parent], i)
			nearest[row.id] = row.id
		} else {
			nearest[row.id] = parent
		}
	}
	multipleRoots := len(children[""]) > 1
	for i := range rows {
		row := &rows[i]
		siblings := children[row.parentID]
		row.nChildren = len(children[row.id])
		row.parentMultipleChildren = len(siblings) > 1
		row.isLast = siblings[len(siblings)-1] == i
		row.isVirtualRootChild = row.parentID == "" && multipleRoots
		row.showConnector = row.parentMultipleChildren && row.parentID != ""
		indent := 0
		row.gutters = nil
		if row.parentID == "" {
			if multipleRoots {
				indent = 1
			}
		} else {
			parent := rows[indices[row.parentID]]
			indent = parent.depth
			if parent.nChildren > 1 || (parent.parentMultipleChildren && indent > 0) {
				indent++
			}
			row.gutters = parent.gutters
			if parent.showConnector {
				row.gutters = append(slices.Clone(parent.gutters), gutterInfo{position: max(parent.displayIndent-1, 0), show: !parent.isLast})
			}
		}
		row.depth = indent
		row.displayIndent = indent
		if multipleRoots {
			row.displayIndent = max(0, indent-1)
		}
		row.prefix = buildTreePrefix(row.displayIndent, row.showConnector, row.isLast, row.gutters)
	}
	// computeFoldable
	parentByID := map[string]string{}
	for _, r := range t.allRows {
		parentByID[r.id] = r.parentID
	}
	visible := map[string]bool{}
	for _, r := range rows {
		visible[r.id] = true
	}
	visibleParentOf := map[string]string{}
	for _, r := range rows {
		cur := parentByID[r.id]
		for cur != "" && !visible[cur] {
			cur = parentByID[cur]
		}
		visibleParentOf[r.id] = cur
	}
	childCount := map[string]int{}
	for _, r := range rows {
		if vp := visibleParentOf[r.id]; vp != "" {
			childCount[vp]++
		}
	}
	foldable := map[string]bool{}
	for _, r := range rows {
		if r.nChildren == 0 {
			continue
		}
		vp := visibleParentOf[r.id]
		foldable[r.id] = vp == "" || childCount[vp] > 1
	}
	return referenceState{rows: rows, cursor: cursor, lastSelectedID: lastSelected, foldable: foldable}
}

// rowsEqual compares the geometry recomputeVisible derives. The ordinal bookkeeping fields are not part of what the reference models.
func rowsEqual(a, b []treeRow) (int, bool) {
	if len(a) != len(b) {
		return -1, false
	}
	for i := range a {
		x, y := a[i], b[i]
		x.ord, y.ord = 0, 0
		if !reflect.DeepEqual(x, y) {
			return i, false
		}
	}
	return 0, true
}

// Randomized equivalence of recomputeVisible against the string-keyed algorithm it replaced (handoffs/P1-real-session.md proposal 5): over generated
// forests, every filter mode, multi-token and no-match queries, fold sets (including folded rows the filter hides), a moving cursor and label-cache
// invalidation, the visible rows with their prefixes, gutters and child counts, the cursor, the remembered selection and the foldable rows are
// identical.
// mutation-checked: see TestTreeRecomputeEquivalenceKillsMutants' notes in the commit message.
func TestTreeRecomputeMatchesTheStringKeyedAlgorithm(t *testing.T) {
	queries := []string{"", "error", "er", "parser test", "ÉCOLE", "école", "i̇stanbul", "  ", "zzz", "entry 1", "alpha  beta", "commit gamma deploy"}
	for seed := uint64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed*7919))
		size := 1 + rng.IntN(260)
		root, nodes := randomRecomputeForest(rng, size)
		list := NewTreeSelectorComponent("t", root).TreeList
		for step := range 60 {
			switch rng.IntN(6) {
			case 0:
				list.filterMode = list.filterModes[rng.IntN(len(list.filterModes))]
				clear(list.foldedNodes)
			case 1:
				list.searchQuery = queries[rng.IntN(len(queries))]
			case 2:
				if len(list.allRows) > 0 {
					id := list.allRows[rng.IntN(len(list.allRows))].id
					list.foldedNodes[id] = !list.foldedNodes[id]
				}
			case 3:
				list.cursor = rng.IntN(len(list.rows)+2) - 1
				list.lastSelectedId = ""
			case 4:
				if len(list.allRows) > 0 {
					id := list.allRows[rng.IntN(len(list.allRows))].id
					list.invalidateNodeCaches(id)
				}
			case 5:
				// A label save changes a node's tags and searchable text and then drops what was memoized about it.
				node := nodes[rng.IntN(len(nodes))]
				node.tags = recomputeTagSets[rng.IntN(len(recomputeTagSets))]
				node.search += " " + recomputeWords[rng.IntN(len(recomputeWords))]
				list.invalidateNodeCaches(node.id)
			}
			want := referenceRecompute(list)
			list.recomputeVisible()
			if at, ok := rowsEqual(list.rows, want.rows); !ok {
				t.Fatalf("seed %d step %d (mode %q query %q): rows differ at %d\n got %d rows\nwant %d rows", seed, step, list.filterMode, list.searchQuery, at, len(list.rows), len(want.rows))
			}
			if list.cursor != want.cursor || list.lastSelectedId != want.lastSelectedID {
				t.Fatalf("seed %d step %d: cursor %d selected %q, want %d %q", seed, step, list.cursor, list.lastSelectedId, want.cursor, want.lastSelectedID)
			}
			for _, r := range list.rows {
				if list.isFoldable(r) != want.foldable[r.id] {
					t.Fatalf("seed %d step %d: isFoldable(%s) = %v, want %v", seed, step, r.id, list.isFoldable(r), want.foldable[r.id])
				}
			}
		}
	}
}

// BenchmarkTreeRecompute measures one search keystroke's recomputeVisible over a session-sized tree (46k rows): handoffs/P1-real-session.md measured
// 29-36 ms and about 20 MB of allocation per call on the string-keyed maps.
func BenchmarkTreeRecompute(b *testing.B) {
	rng := rand.New(rand.NewPCG(7, 11))
	list := NewTreeSelectorComponent("t", randomRecomputeTree(rng, 46000)).TreeList
	queries := []string{"e", "er", "err", "erro", "error"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		list.searchQuery = queries[i%len(queries)]
		list.recomputeVisible()
	}
}
