package codingagent

import "github.com/MichaelKinsy/PiG/tui"

// NewTreeSelectorComponent is Pi's TreeSelectorComponent constructor (tree-selector.ts constructor): it takes the session tree as
// SessionTreeNode data and reads the tool-call arguments and the current leaf from that tree, as Pi's flattenTree does
// (tree-selector.ts:202-262). The tui selector is the engine; tui cannot import this package's SessionTreeNode, so the adapter that
// presents the data as tui.TreeNode rows lives here.
func NewTreeSelectorComponent(tree []*SessionTreeNode, currentLeafID *string, terminalHeight int, onSelect func(entryID string), onCancel func(), onLabelChange func(entryID string, label *string), initialSelectedID *string, initialFilterMode string) *tui.TreeSelectorComponent {
	formatter := newTreeRowFormatterFromTree(tree, currentLeafID)
	roots := (&treeNodeAdapter{n: &SessionTreeNode{Children: tree}, f: formatter}).NodeChildren()
	return tui.NewTreeSelectorComponentFrom(roots, currentLeafID, terminalHeight, onSelect, onCancel, onLabelChange, initialSelectedID, initialFilterMode)
}
