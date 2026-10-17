package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's TreeList render (modes/interactive/components/tree-selector.js) with a typed search query runs against the vendored Pi module: the visible rows, search matching (JavaScript lowercasing and \s splitting), row text and the status line must agree for every chain in the shared tree corpus.
func TestTreeListSearchRenderMatchesPiOracle(t *testing.T) {
	useTreeOracleTUIDefaults(t)
	t.Setenv("HOME", "/home/oracle")
	t.Setenv("USERPROFILE", "")
	chains := treeOracleChains()
	queries := []string{"", "user", "ASSISTANT", "assistant aborted", "tool", "bash", "ls", "title", "compaction", "branch summary", "branch  summary", "label", "L001", "model", "[", "小小", "😀", "x" + strings.Repeat("😀", 5), "İ", "ß", "\u00a0nbsp", "a\ufeffb", "a\u0085b", "hello there", "line1", "zzzzqqqq", "ct", "ext", "\u2003em", "thinking high"}
	type input struct {
		Chains  [][]map[string]any `json:"chains"`
		Queries []string           `json:"queries"`
	}
	var want [][]string
	pioracle.Run(t, `
const th = await load("pi-coding-agent/modes/interactive/theme/theme.js");
th.initTheme("dark", false);
const tui = await load("../pi-tui.mjs");
const { KeybindingsManager } = await load("pi-coding-agent/core/keybindings.js");
tui.setKeybindings(new KeybindingsManager({}));
const mod = await load("pi-coding-agent/modes/interactive/components/tree-selector.js");
emit(input.chains.map((chain, ci) => input.queries.map((q) => {
	const nodes = chain.map((entry) => ({ entry, children: [], label: ci % 2 === 1 ? "L" + String(ci).padStart(3, "0") : undefined }));
	nodes.forEach((node, i) => { if (nodes[i + 1]) node.children.push(nodes[i + 1]); });
	const c = new mod.TreeSelectorComponent([nodes[0]], chain[chain.length - 1].id, 20, () => {}, () => {}, () => {});
	if (q) c.treeList.handleInput(q);
	return c.treeList.render(100).join("\n");
})));`, input{chains, queries}, &want, treeOracleEnv...)
	mismatches := 0
	for i, chain := range chains {
		for k, query := range queries {
			nodes := make([]*SessionTreeNode, len(chain))
			for j, fields := range chain {
				nodes[j] = &SessionTreeNode{Entry: mustEntry(t, fields)}
				if i%2 == 1 {
					nodes[j].Label = fmtTreeLabel(i)
				}
				if j > 0 {
					nodes[j-1].Children = []*SessionTreeNode{nodes[j]}
				}
			}
			leaf := chain[len(chain)-1]["id"].(string)
			selector := NewTreeSelectorComponent(nodes[:1], &leaf, 20, func(string) {}, func() {}, nil, nil, "")
			list := selector.GetTreeList()
			if query != "" {
				list.HandleInput(query)
			}
			got := strings.ToValidUTF8(strings.Join(list.Render(100), "\n"), "\ufffd")
			if got != want[i][k] {
				if mismatches++; mismatches <= 6 {
					t.Errorf("chain %d query %q\n Go %q\n Pi %q", i, query, got, want[i][k])
				}
			}
		}
	}
	if mismatches > 6 {
		t.Errorf("%d mismatching renders", mismatches)
	}
}

func fmtTreeLabel(i int) string { return "L" + strings.Repeat("0", 3-len(itoaPlain(i))) + itoaPlain(i) }

func itoaPlain(i int) string {
	digits := ""
	for {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
		if i == 0 {
			return digits
		}
	}
}
