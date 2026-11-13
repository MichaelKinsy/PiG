package hljs

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// treeNode is a TokenTree data node. A child is either text or a nested node.
type treeNode struct {
	kind        string
	sublanguage bool
	children    []treeChild
}

type treeChild struct {
	text string
	node *treeNode
}

// tokenTreeEmitter is highlight.js TokenTreeEmitter. appends counts the children added to any node, and continued the nodes a run that continues inside open modes opened before its first child (reopenNodes): the HTML before the run holds their opening tags.
type tokenTreeEmitter struct {
	root      *treeNode
	stack     []*treeNode
	appends   int
	continued int
}

func newTokenTreeEmitter() *tokenTreeEmitter {
	root := &treeNode{}
	return &tokenTreeEmitter{root: root, stack: []*treeNode{root}}
}

func (e *tokenTreeEmitter) top() *treeNode { return e.stack[len(e.stack)-1] }

func (e *tokenTreeEmitter) add(child treeChild) {
	top := e.top()
	top.children = append(top.children, child)
	e.appends++
}

func (e *tokenTreeEmitter) openNode(kind string) {
	node := &treeNode{kind: kind}
	e.add(treeChild{node: node})
	e.stack = append(e.stack, node)
}

func (e *tokenTreeEmitter) closeNode() bool {
	if len(e.stack) > 1 {
		e.stack = e.stack[:len(e.stack)-1]
		return true
	}
	return false
}

func (e *tokenTreeEmitter) closeAllNodes() {
	for e.closeNode() {
	}
}

func (e *tokenTreeEmitter) addKeyword(text, kind string) {
	if text == "" {
		return
	}
	e.openNode(kind)
	e.addText(text)
	e.closeNode()
}

func (e *tokenTreeEmitter) addText(text string) {
	if text == "" {
		return
	}
	e.add(treeChild{text: text})
}

// addSublanguage adds another emitter's root as a sublanguage node named after its language; an undefined language leaves the kind empty.
func (e *tokenTreeEmitter) addSublanguage(other *tokenTreeEmitter, name string) {
	node := other.root
	node.kind = name
	node.sublanguage = true
	e.add(treeChild{node: node})
}

// toHTML is highlight.js HTMLRenderer with classPrefix "hljs-". Concatenation joins the halves of a surrogate pair that a token boundary split, as it does for JavaScript strings.
func (e *tokenTreeEmitter) toHTML() string {
	var buffer strings.Builder
	walkHTML(&buffer, e.root)
	return jsstring.Canonical(buffer.String())
}

// checkpointHTML is the HTML of the tree without the opening tags of the continued nodes, and for each checkpoint, in their order, the length of that HTML at the checkpoint: after the children added before it, inside the nodes it leaves open. Children are added in document order, so one walk passes every checkpoint in turn; ok is false when it passes fewer than there are.
func (e *tokenTreeEmitter) checkpointHTML(checkpoints []checkpoint) (html string, offsets []int, ok bool) {
	walk := checkpointWalk{checkpoints: checkpoints, offsets: make([]int, 0, len(checkpoints))}
	node := e.root
	for range e.continued {
		node = node.children[0].node
		walk.continued = append(walk.continued, node)
	}
	walk.mark(0)
	walk.node(e.root, 0)
	return walk.buffer.String(), walk.offsets, len(walk.offsets) == len(checkpoints)
}

// checkpointWalk writes the HTML of a tree and notes its length at each checkpoint. continued is the nodes whose opening tags it leaves out, outermost first, and added the children it has passed.
type checkpointWalk struct {
	buffer      strings.Builder
	checkpoints []checkpoint
	offsets     []int
	continued   []*treeNode
	added       int
}

// mark notes the length of the HTML for the checkpoints that stand after the children passed, with depth nodes open.
func (w *checkpointWalk) mark(depth int) {
	for next := len(w.offsets); next < len(w.checkpoints) && int(w.checkpoints[next].appends) == w.added && int(w.checkpoints[next].depth) == depth; next++ {
		w.offsets = append(w.offsets, w.buffer.Len())
	}
}

// node writes the children of a node that is depth nodes below the root.
func (w *checkpointWalk) node(node *treeNode, depth int) {
	for _, child := range node.children {
		w.added++
		switch {
		case child.node == nil:
			w.buffer.WriteString(escapeHTML(child.text))
		case child.node.sublanguage:
			walkHTML(&w.buffer, child.node)
		default:
			if depth >= len(w.continued) || w.continued[depth] != child.node {
				writeOpenTag(&w.buffer, child.node)
			}
			w.mark(depth + 1)
			w.node(child.node, depth+1)
			writeCloseTag(&w.buffer, child.node)
		}
		w.mark(depth)
	}
}

func writeOpenTag(buffer *strings.Builder, node *treeNode) {
	if node.kind == "" {
		return
	}
	buffer.WriteString(`<span class="`)
	if !node.sublanguage {
		buffer.WriteString("hljs-")
	}
	buffer.WriteString(node.kind)
	buffer.WriteString(`">`)
}

func writeCloseTag(buffer *strings.Builder, node *treeNode) {
	if node.kind != "" {
		buffer.WriteString("</span>")
	}
}

func walkHTML(buffer *strings.Builder, node *treeNode) {
	writeOpenTag(buffer, node)
	for _, child := range node.children {
		if child.node != nil {
			walkHTML(buffer, child.node)
			continue
		}
		buffer.WriteString(escapeHTML(child.text))
	}
	writeCloseTag(buffer, node)
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")

// escapeHTML is highlight.js escapeHTML.
func escapeHTML(value string) string { return htmlEscaper.Replace(value) }
