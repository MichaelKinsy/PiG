package codingagent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's TreeList (modes/interactive/components/tree-selector.js) runs against the vendored Pi module over random branching session trees (several roots, folded branches, a current leaf anywhere) driven by random key sequences: movement and paging, fold and unfold, every filter mode, label timestamps and typed search. The rendered rows, gutters, connectors, fold markers and status line must agree byte for byte.
func TestTreeListBranchingRenderMatchesPiOracle(t *testing.T) {
	useTreeOracleTUIDefaults(t)
	t.Setenv("HOME", "/home/oracle")
	t.Setenv("USERPROFILE", "")
	var pool []map[string]any
	for _, chain := range treeOracleChains() {
		pool = append(pool, chain...)
	}
	rng := rand.New(rand.NewSource(21))
	keys := []string{"\x1b[A", "\x1b[B", "\x1b[A", "\x1b[B", "\x1b[B", "\x1b[1;5D", "\x1b[1;5C", "\x1b[1;5D", "\x1b[D", "\x1b[C", "\x1b[5~", "\x1b[6~", "\x04", "\x14", "\x15", "\x0c", "\x01", "\x0f", "T", "a", "u", "bash", "x", "\x7f"}
	type tree struct {
		Entries   []map[string]any `json:"entries"`
		Labels    map[string]any   `json:"labels"`
		Leaf      string           `json:"leaf"`
		Selected  *string          `json:"selected"`
		Filter    *string          `json:"filter"`
		Height    int              `json:"height"`
		KeySteps  [][]string       `json:"keySteps"`
		RootCount int              `json:"rootCount"`
	}
	var trees []tree
	for range 90 {
		n := 2 + rng.Intn(14)
		tr := tree{Labels: map[string]any{}, Height: []int{8, 12, 20, 30}[rng.Intn(4)]}
		for i := range n {
			source := pool[rng.Intn(len(pool))]
			raw, _ := json.Marshal(source)
			var entry map[string]any
			_ = json.Unmarshal(raw, &entry)
			entry["id"] = fmt.Sprintf("n%d", i)
			entry["parentId"] = nil
			if i > 0 && rng.Intn(8) != 0 {
				entry["parentId"] = fmt.Sprintf("n%d", rng.Intn(i))
			}
			if message, ok := entry["message"].(map[string]any); ok && message["role"] == "assistant" && rng.Intn(2) == 0 {
				if content, ok := message["content"].([]any); ok {
					for _, block := range content {
						if call, ok := block.(map[string]any); ok && call["type"] == "toolCall" {
							call["id"] = fmt.Sprintf("tc%d", i)
						}
					}
				}
			}
			if message, ok := entry["message"].(map[string]any); ok && message["role"] == "toolResult" && i > 0 {
				message["toolCallId"] = fmt.Sprintf("tc%d", rng.Intn(i))
			}
			if rng.Intn(5) == 0 {
				tr.Labels[fmt.Sprintf("n%d", i)] = []any{fmt.Sprintf("lab%d", i), []string{"2026-01-01T10:30:00.000Z", "2024-05-06T07:08:00.000Z", "2020-12-31T23:59:00.000Z"}[rng.Intn(3)]}
			}
			tr.Entries = append(tr.Entries, entry)
		}
		tr.Leaf = fmt.Sprintf("n%d", rng.Intn(n))
		if rng.Intn(4) == 0 {
			tr.Selected = new(fmt.Sprintf("n%d", rng.Intn(n)))
		}
		if rng.Intn(3) == 0 {
			tr.Filter = new([]string{"default", "no-tools", "user-only", "labeled-only", "all"}[rng.Intn(5)])
		}
		tr.KeySteps = append(tr.KeySteps, []string{})
		for range 5 {
			steps := make([]string, rng.Intn(7))
			for i := range steps {
				steps[i] = keys[rng.Intn(len(keys))]
			}
			tr.KeySteps = append(tr.KeySteps, steps)
		}
		trees = append(trees, tr)
	}
	// Upstream's selectedIndex is not confined to the rows: a key pressed over an empty filtered list leaves it at -1 (up, page down) or 1 (down), and the next filter change keeps it there while no row was ever selected (tree-selector.ts handleInput, applyFilter). A user-only filter over a tree without user messages is such a list.
	var notUser []map[string]any
	for _, source := range pool {
		if message, ok := source["message"].(map[string]any); !ok || message["role"] != "user" {
			notUser = append(notUser, source)
		}
	}
	emptyListKeys := [][]string{
		{"\x1b[A", "\x04"},
		{"\x1b[B", "\x04"},
		{"\x1b[6~", "\x04"},
		{"\x1b[5~", "\x04"},
		{"\x1b[A", "\x1b[A", "\x04", "\x1b[B"},
		{"\x1b[A", "\x04", "\x1b[A", "\x1b[A"},
		{"\x1b[B", "\x1b[B", "\x04", "\x1b[A"},
		{"\x1b[B", "\x04", "\r"},
		{"\x1b[A", "\x04", "\x1b[1;5D", "\x1b[1;5C", "\x1b[D", "\x1b[C"},
		{"\x1b[6~", "\x04", "\x1b[B"},
	}
	for ti, keySteps := range emptyListKeys {
		tr := tree{Labels: map[string]any{}, Height: []int{3, 12}[ti%2], Leaf: "n1", Filter: new("user-only")}
		for i := range 3 {
			raw, _ := json.Marshal(notUser[(ti+i)%len(notUser)])
			var entry map[string]any
			_ = json.Unmarshal(raw, &entry)
			entry["id"] = fmt.Sprintf("n%d", i)
			entry["parentId"] = nil
			if i > 0 {
				entry["parentId"] = fmt.Sprintf("n%d", i-1)
			}
			tr.Entries = append(tr.Entries, entry)
		}
		tr.KeySteps = [][]string{keySteps}
		trees = append(trees, tr)
	}
	var want [][]string
	pioracle.Run(t, `
const th = await load("pi-coding-agent/modes/interactive/theme/theme.js");
th.initTheme("dark", false);
const tui = await load("../pi-tui.mjs");
const { KeybindingsManager } = await load("pi-coding-agent/core/keybindings.js");
tui.setKeybindings(new KeybindingsManager({}));
const mod = await load("pi-coding-agent/modes/interactive/components/tree-selector.js");
emit(input.map((tr) => tr.keySteps.map((steps) => {
	const byId = new Map();
	const roots = [];
	for (const entry of tr.entries) {
		const lab = tr.labels[entry.id];
		const node = { entry, children: [], label: lab ? lab[0] : undefined, labelTimestamp: lab ? lab[1] : undefined };
		byId.set(entry.id, node);
	}
	for (const entry of tr.entries) {
		const node = byId.get(entry.id);
		const parent = entry.parentId ? byId.get(entry.parentId) : undefined;
		if (parent) parent.children.push(node); else roots.push(node);
	}
	const c = new mod.TreeSelectorComponent(roots, tr.leaf, tr.height, () => {}, () => {}, () => {}, tr.selected ?? undefined, tr.filter ?? undefined);
	for (const key of steps) c.treeList.handleInput(key);
	return c.treeList.render(100).join("\n");
})));`, trees, &want, treeOracleEnv...)
	mismatches := 0
	for i, tr := range trees {
		for k, steps := range tr.KeySteps {
			byID := map[string]*SessionTreeNode{}
			var roots []*SessionTreeNode
			for _, fields := range tr.Entries {
				node := &SessionTreeNode{Entry: mustEntry(t, fields)}
				if lab, ok := tr.Labels[fields["id"].(string)].([]any); ok {
					node.Label, node.LabelTimestamp = lab[0].(string), lab[1].(string)
				}
				byID[fields["id"].(string)] = node
			}
			for _, fields := range tr.Entries {
				node := byID[fields["id"].(string)]
				if parent, ok := fields["parentId"].(string); ok {
					byID[parent].Children = append(byID[parent].Children, node)
				} else {
					roots = append(roots, node)
				}
			}
			leaf := tr.Leaf
			filter := ""
			if tr.Filter != nil {
				filter = *tr.Filter
			}
			selector := NewTreeSelectorComponent(roots, &leaf, tr.Height, func(string) {}, func() {}, nil, tr.Selected, filter)
			list := selector.GetTreeList()
			for _, key := range steps {
				list.HandleInput(key)
			}
			got := strings.ToValidUTF8(strings.Join(list.Render(100), "\n"), "\ufffd")
			if got != want[i][k] {
				if mismatches++; mismatches <= 12 {
					selected, filterName := "", ""
					if tr.Selected != nil {
						selected = *tr.Selected
					}
					if tr.Filter != nil {
						filterName = *tr.Filter
					}
					t.Errorf("tree %d steps %q leaf %s selected %q height %d filter %q: Go/Pi differ (%d vs %d bytes)", i, steps, tr.Leaf, selected, tr.Height, filterName, len(got), len(want[i][k]))
					if testing.Verbose() {
						for _, e := range tr.Entries {
							m, _ := e["message"].(map[string]any)
							c, _ := json.Marshal(m["content"])
							if len(c) > 90 {
								c = c[:90]
							}
							t.Logf("  %v parent=%v type=%v role=%v stop=%v content=%s err=%v", e["id"], e["parentId"], e["type"], m["role"], m["stopReason"], c, m["errorMessage"] != nil)
						}
						t.Logf("\n Go %q\n Pi %q", got, want[i][k])
					}
				}
			}
		}
	}
	if mismatches > 12 {
		t.Errorf("%d mismatching renders of %d", mismatches, len(trees)*6)
	}
}
