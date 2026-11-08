package components

// Adapted from coding-agent interactive/components/tree-selector.ts (`/tree`, docs/tree.md): the tree layout (indent at branch points,
// connectors, gutters, the current branch first), the active path marker, selection, paging and the label input. Changes: nodes are p2's tree
// nodes (`plugins/tree/contract.ts`: user messages and answers by entry ID, no tool entries), so there are no filters, folding or copying;
// labels are branch labels, shown on each branch's first own message and edited for the branch holding the selected message; the instance
// theme and keymap (section 5.4) replace the globals; message text and labels are server text (`LineText`).
//
// pi: apps/p2/src/tui/components/tree-selector.ts

import (
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TreeSelectorNode is one message of the tree; Parent is the node before it on its branch (nil for none), Conversation the branch storing it.
type TreeSelectorNode struct {
	Entry        int
	Parent       *int
	Conversation int
	// Role is "user" or "assistant".
	Role string
	Text string
}

type treeNode struct {
	node     *TreeSelectorNode
	children []*treeNode
}

// gutter is the indent level where a connector was shown, and whether its branch goes on below (`│`).
type gutter struct {
	position int
	show     bool
}

type flatNode struct {
	node   *TreeSelectorNode
	indent int
	// showConnector is a connector (`├─`/`└─`): its parent has several children.
	showConnector bool
	isLast        bool
	gutters       []gutter
	// isVirtualRootChild is a root among several: shown without a connector.
	isVirtualRootChild bool
}

type treeList struct {
	flat          []flatNode
	activePath    map[int]bool
	multipleRoots bool
	selectedIndex int
	ctx           SelectorContext
	maxVisible    int
	// labels are the branch labels by conversation (null labels already resolved to the session title).
	labels map[int]string
	// firsts are the entries starting a branch: the first of a conversation's own nodes, where its label shows.
	firsts      map[int]bool
	onSelect    func(entry int)
	onCancel    func()
	onLabelEdit func(conversation int)
}

func newTreeList(ctx SelectorContext, nodes []TreeSelectorNode, current *int, labels map[int]string, maxVisible int) *treeList {
	l := &treeList{ctx: ctx, maxVisible: maxVisible, labels: map[int]string{}, activePath: map[int]bool{}, firsts: map[int]bool{}}
	for key, label := range labels {
		l.labels[key] = label
	}
	byEntry := make(map[int]*TreeSelectorNode, len(nodes))
	for i := range nodes {
		byEntry[nodes[i].Entry] = &nodes[i]
	}
	for at := current; at != nil; {
		l.activePath[*at] = true
		node := byEntry[*at]
		if node == nil {
			break
		}
		at = node.Parent
	}
	for i := range nodes {
		node := &nodes[i]
		if node.Parent == nil || byEntry[*node.Parent] == nil || byEntry[*node.Parent].Conversation != node.Conversation {
			l.firsts[node.Entry] = true
		}
	}
	roots := buildTree(nodes)
	l.multipleRoots = len(roots) > 1
	l.flat = l.flatten(roots)
	index := -1
	if current != nil {
		for i, each := range l.flat {
			if each.node.Entry == *current {
				index = i
				break
			}
		}
	}
	if index >= 0 {
		l.selectedIndex = index
	} else {
		l.selectedIndex = max(0, len(l.flat)-1)
	}
	return l
}

type flattenItem struct {
	tree               *treeNode
	indent             int
	justBranched       bool
	showConnector      bool
	isLast             bool
	gutters            []gutter
	isVirtualRootChild bool
}

// flatten is coding-agent's `flattenTree`: indent +1 where a parent branches (and once more for the first generation after a branch), flat for
// single-child chains; the subtree holding the current branch comes first.
func (l *treeList) flatten(roots []*treeNode) []flatNode {
	var result []flatNode
	// Children before parents (pre-order reversed), without recursion: a long session is one deep chain.
	containsActive := map[*treeNode]bool{}
	var preOrder []*treeNode
	for pending := append([]*treeNode(nil), roots...); len(pending) > 0; {
		tree := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		preOrder = append(preOrder, tree)
		pending = append(pending, tree.children...)
	}
	for i := len(preOrder) - 1; i >= 0; i-- {
		tree := preOrder[i]
		active := l.activePath[tree.node.Entry]
		for _, child := range tree.children {
			if containsActive[child] {
				active = true
				break
			}
		}
		containsActive[tree] = active
	}
	first := func(list []*treeNode) []*treeNode {
		sorted := append([]*treeNode(nil), list...)
		slices.SortStableFunc(sorted, func(a, b *treeNode) int { return boolNumber(containsActive[b]) - boolNumber(containsActive[a]) })
		return sorted
	}
	var stack []flattenItem
	multiple := len(roots) > 1
	orderedRoots := first(roots)
	rootIndent := 0
	if multiple {
		rootIndent = 1
	}
	for i := len(orderedRoots) - 1; i >= 0; i-- {
		stack = append(stack, flattenItem{orderedRoots[i], rootIndent, multiple, multiple, i == len(orderedRoots)-1, nil, multiple})
	}
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		result = append(result, flatNode{item.tree.node, item.indent, item.showConnector, item.isLast, item.gutters, item.isVirtualRootChild})
		children := first(item.tree.children)
		branches := len(children) > 1
		childIndent := item.indent
		if branches || (item.justBranched && item.indent > 0) {
			childIndent = item.indent + 1
		}
		displayIndent := item.indent
		if l.multipleRoots {
			displayIndent = max(0, item.indent-1)
		}
		childGutters := item.gutters
		if item.showConnector && !item.isVirtualRootChild {
			childGutters = append(append([]gutter(nil), item.gutters...), gutter{max(0, displayIndent-1), !item.isLast})
		}
		for i := len(children) - 1; i >= 0; i-- {
			stack = append(stack, flattenItem{children[i], childIndent, branches, branches, i == len(children)-1, childGutters, false})
		}
	}
	return result
}

func (l *treeList) Invalidate() {}

func (l *treeList) Render(width int) []string {
	theme := l.ctx.Theme().Current()
	if len(l.flat) == 0 {
		return []string{theme.Fg("muted", "  No messages yet")}
	}
	start := max(0, min(l.selectedIndex-l.maxVisible/2, len(l.flat)-l.maxVisible))
	end := min(start+l.maxVisible, len(l.flat))
	var lines []string
	for i := start; i < end; i++ {
		flat := l.flat[i]
		selected := i == l.selectedIndex
		displayIndent := flat.indent
		if l.multipleRoots {
			displayIndent = max(0, flat.indent-1)
		}
		connector := flat.showConnector && !flat.isVirtualRootChild
		connectorAt := -1
		if connector {
			connectorAt = displayIndent - 1
		}
		var prefix strings.Builder
		for level := range displayIndent {
			found := -1
			for gi, each := range flat.gutters {
				if each.position == level {
					found = gi
					break
				}
			}
			switch {
			case found >= 0:
				if flat.gutters[found].show {
					prefix.WriteString("│  ")
				} else {
					prefix.WriteString("   ")
				}
			case level == connectorAt:
				if flat.isLast {
					prefix.WriteString("└─ ")
				} else {
					prefix.WriteString("├─ ")
				}
			default:
				prefix.WriteString("   ")
			}
		}
		marker := ""
		if l.activePath[flat.node.Entry] {
			marker = theme.Fg("accent", "• ")
		}
		labelText := ""
		if l.firsts[flat.node.Entry] {
			if label, ok := l.labels[flat.node.Conversation]; ok {
				labelText = theme.Fg("warning", "["+render.LineText(label)+"] ")
			}
		}
		role := theme.Fg("muted", "assistant: ")
		if flat.node.Role == "user" {
			role = theme.Fg("accent", "user: ")
		}
		body := render.LineText(flat.node.Text)
		if selected {
			body = theme.Bold(body)
		}
		full := theme.Fg("dim", prefix.String()) + marker + labelText + role + body
		cursor := "  "
		if selected {
			cursor = theme.Fg("accent", "› ")
		}
		line := widthx.TruncateToWidth(cursor+full, width, "…", false)
		if selected {
			line = theme.Bg("selectedBg", line)
		}
		lines = append(lines, line)
	}
	return append(lines, theme.Fg("muted", "  ("+strconv.Itoa(l.selectedIndex+1)+"/"+strconv.Itoa(len(l.flat))+")"))
}

func (l *treeList) HandleInput(data string) {
	keys := l.ctx.Keys()
	last := len(l.flat) - 1
	switch {
	case keys.Matches(data, "tui.select.up"):
		if l.selectedIndex == 0 {
			l.selectedIndex = last
		} else {
			l.selectedIndex--
		}
	case keys.Matches(data, "tui.select.down"):
		if l.selectedIndex == last {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
	case keys.Matches(data, "tui.select.pageUp") || keys.Matches(data, "tui.editor.cursorLeft"):
		l.selectedIndex = max(0, l.selectedIndex-l.maxVisible)
	case keys.Matches(data, "tui.select.pageDown") || keys.Matches(data, "tui.editor.cursorRight"):
		l.selectedIndex = min(last, l.selectedIndex+l.maxVisible)
	case keys.Matches(data, "tui.select.confirm"):
		if l.selectedIndex >= 0 && l.selectedIndex < len(l.flat) && l.onSelect != nil {
			l.onSelect(l.flat[l.selectedIndex].node.Entry)
		}
	case keys.Matches(data, "app.tree.editLabel"):
		if l.selectedIndex >= 0 && l.selectedIndex < len(l.flat) && l.onLabelEdit != nil {
			l.onLabelEdit(l.flat[l.selectedIndex].node.Conversation)
		}
	case keys.Matches(data, "tui.select.cancel"):
		if l.onCancel != nil {
			l.onCancel()
		}
	}
	l.ctx.RequestRender()
}

// buildTree builds nodes (parents first) as trees: each node below its `parent`; nodes without a known parent are roots.
func buildTree(nodes []TreeSelectorNode) []*treeNode {
	trees := make(map[int]*treeNode, len(nodes))
	var roots []*treeNode
	for i := range nodes {
		node := &nodes[i]
		tree := &treeNode{node: node}
		trees[node.Entry] = tree
		var parent *treeNode
		if node.Parent != nil {
			parent = trees[*node.Parent]
		}
		if parent != nil {
			parent.children = append(parent.children, tree)
		} else {
			roots = append(roots, tree)
		}
	}
	return roots
}

// labelInput is pi's label input, for a branch: Enter saves the trimmed text (empty: see TreeSelectorComponent), Escape cancels.
type labelInput struct {
	input    *tui.TextInput
	ctx      SelectorContext
	prompt   string
	onSubmit func(label string)
	onCancel func()
}

func newLabelInput(ctx SelectorContext, prompt, current string) *labelInput {
	l := &labelInput{input: tui.NewInput(tui.InputOptions{}), ctx: ctx, prompt: prompt}
	// As a paste: the cursor ends after the text (`setValue` leaves it at the start), and server text is never keys.
	l.input.HandleInput("\x1b[200~" + current + "\x1b[201~")
	return l
}

// SetFocused is the TypeScript's `focused` setter: focus reaches the input for IME cursor placement.
func (l *labelInput) SetFocused(focused bool) { l.input.SetFocused(focused) }

func (l *labelInput) Invalidate() {}

func (l *labelInput) Render(width int) []string {
	theme := l.ctx.Theme().Current()
	const indent = "  "
	lines := []string{widthx.TruncateToWidth(indent+theme.Fg("muted", l.prompt), width, "…", false)}
	for _, line := range l.input.Render(width - len(indent)) {
		lines = append(lines, widthx.TruncateToWidth(indent+line, width, "…", false))
	}
	return append(lines, widthx.TruncateToWidth(indent+render.KeyHint(l.ctx, "tui.select.confirm", "save")+"  "+render.KeyHint(l.ctx, "tui.select.cancel", "cancel"), width, "…", false))
}

func (l *labelInput) HandleInput(data string) {
	keys := l.ctx.Keys()
	switch {
	case keys.Matches(data, "tui.select.confirm"):
		if l.onSubmit != nil {
			l.onSubmit(strings.TrimFunc(l.input.GetValue(), widthx.IsJSSpace))
		}
	case keys.Matches(data, "tui.select.cancel"):
		if l.onCancel != nil {
			l.onCancel()
		}
	default:
		l.input.HandleInput(data)
	}
	l.ctx.RequestRender()
}

// TreeSelectorComponent is pi's `/tree` selector over the session's branches: Enter continues the session at the chosen message; the label
// key renames the branch holding the selected message (onRename; empty resets conversation 1 to the session title). The owner of the selector
// shows a saved label with SetLabel.
type TreeSelectorComponent struct {
	*tui.Container
	ctx            SelectorContext
	list           *treeList
	listContainer  *tui.Container
	labelContainer *tui.Container
	labelInput     *labelInput
	focused        bool
}

// NewTreeSelector is the TreeSelectorComponent constructor; current is nil for no current entry.
func NewTreeSelector(ctx SelectorContext, nodes []TreeSelectorNode, current *int, labels map[int]string, onSelect func(entry int), onCancel func(), onRename func(conversation int, label string)) *TreeSelectorComponent {
	c := &TreeSelectorComponent{Container: tui.NewContainer(), ctx: ctx, listContainer: tui.NewContainer(), labelContainer: tui.NewContainer()}
	theme := ctx.Theme().Current()
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewPaddedText(theme.Bold("Session Tree"), 1, 0, nil))
	c.AddChild(tui.NewPaddedText(theme.Fg("muted", "Continue from a message: an answer continues after it, a user message is typed again · "+
		render.KeyHint(ctx, "tui.select.confirm", "select")+" · "+render.KeyHint(ctx, "app.tree.editLabel", "label")+" · "+render.KeyHint(ctx, "tui.select.cancel", "cancel")), 1, 0, nil))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder(ctx, ""))
	c.list = newTreeList(ctx, nodes, current, labels, 16)
	c.list.onSelect = onSelect
	c.list.onCancel = onCancel
	c.list.onLabelEdit = func(conversation int) { c.editLabel(conversation, onRename) }
	c.listContainer.AddChild(c.list)
	c.AddChild(c.listContainer)
	c.AddChild(c.labelContainer)
	c.AddChild(NewDynamicBorder(ctx, ""))
	return c
}

// SetFocused is the TypeScript's `focused` setter.
func (c *TreeSelectorComponent) SetFocused(focused bool) {
	c.focused = focused
	if c.labelInput != nil {
		c.labelInput.SetFocused(focused)
	}
}

// SetLabel shows a branch's saved label.
func (c *TreeSelectorComponent) SetLabel(conversation int, label string) {
	c.list.labels[conversation] = label
	c.ctx.RequestRender()
}

// HandleInput routes to the label input while it is open, else to the list.
func (c *TreeSelectorComponent) HandleInput(data string) {
	if c.labelInput != nil {
		c.labelInput.HandleInput(data)
	} else {
		c.list.HandleInput(data)
	}
}

func (c *TreeSelectorComponent) editLabel(conversation int, onRename func(conversation int, label string)) {
	prompt := "Branch label:"
	if conversation == 1 {
		prompt = "Branch label (empty: the session title):"
	}
	input := newLabelInput(c.ctx, prompt, c.list.labels[conversation])
	input.onSubmit = func(label string) {
		c.closeLabel()
		onRename(conversation, label)
	}
	input.onCancel = c.closeLabel
	c.labelInput = input
	input.SetFocused(c.focused)
	c.listContainer.Clear()
	c.labelContainer.AddChild(input)
}

func (c *TreeSelectorComponent) closeLabel() {
	c.labelInput = nil
	c.labelContainer.Clear()
	c.listContainer.AddChild(c.list)
}
