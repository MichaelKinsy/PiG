package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's TreeList.getSearchableText and getEntryCopyText (modes/interactive/components/tree-selector.js) run against the vendored Pi module over the shared tree-entry corpus, with and without a branch label.
func TestTreeSearchAndCopyTextMatchPiOracle(t *testing.T) {
	chains := treeOracleChains()
	type result struct {
		Search string  `json:"search"`
		Copy   *string `json:"copy"`
	}
	var want [][]result
	pioracle.Run(t, `
const th = await load("pi-coding-agent/modes/interactive/theme/theme.js");
th.initTheme("dark", false);
const mod = await load("pi-coding-agent/modes/interactive/components/tree-selector.js");
emit(input.map((chain, ci) => {
	const nodes = chain.map((entry) => ({ entry, children: [], label: ci % 2 === 1 ? "L" + String(ci).padStart(3, "0") : undefined }));
	nodes.forEach((node, i) => { if (nodes[i + 1]) node.children.push(nodes[i + 1]); });
	const c = new mod.TreeSelectorComponent([nodes[0]], chain[chain.length - 1].id, 20, () => {}, () => {}, () => {});
	return nodes.map((node) => ({ search: c.treeList.getSearchableText(node), copy: c.treeList.getEntryCopyText(node) ?? null }));
}));`, chains, &want)
	mismatches := 0
	for i, chain := range chains {
		nodes := make([]*SessionTreeNode, len(chain))
		for j, fields := range chain {
			nodes[j] = &SessionTreeNode{Entry: mustEntry(t, fields)}
			if i%2 == 1 {
				nodes[j].Label = fmt.Sprintf("L%03d", i)
			}
			if j > 0 {
				nodes[j-1].Children = []*SessionTreeNode{nodes[j]}
			}
		}
		leaf := chain[len(chain)-1]["id"].(string)
		formatter := newTreeRowFormatterFromTree(nodes[:1], &leaf)
		for j, node := range nodes {
			adapter := &treeNodeAdapter{n: node, f: formatter}
			got := result{Search: strings.ToValidUTF8(adapter.NodeSearchableText(), "\ufffd"), Copy: adapter.NodeCopyText()}
			if got.Copy != nil {
				valid := strings.ToValidUTF8(*got.Copy, "\ufffd")
				got.Copy = &valid
			}
			w := want[i][j]
			if got.Search != w.Search || (got.Copy == nil) != (w.Copy == nil) || (got.Copy != nil && *got.Copy != *w.Copy) {
				if mismatches++; mismatches <= 10 {
					t.Errorf("%v\n Go search %q copy %v\n Pi search %q copy %v", chain[j], got.Search, treeOracleStr(got.Copy), w.Search, treeOracleStr(w.Copy))
				}
			}
		}
	}
	if mismatches > 10 {
		t.Errorf("%d mismatching nodes", mismatches)
	}
}

func treeOracleStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
