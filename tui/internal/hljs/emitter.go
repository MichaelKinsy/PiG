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

// tokenTreeEmitter is highlight.js TokenTreeEmitter.
type tokenTreeEmitter struct {
	root  *treeNode
	stack []*treeNode
}

func newTokenTreeEmitter() *tokenTreeEmitter {
	root := &treeNode{}
	return &tokenTreeEmitter{root: root, stack: []*treeNode{root}}
}

func (e *tokenTreeEmitter) top() *treeNode { return e.stack[len(e.stack)-1] }

func (e *tokenTreeEmitter) add(child treeChild) {
	top := e.top()
	top.children = append(top.children, child)
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

func walkHTML(buffer *strings.Builder, node *treeNode) {
	wraps := node.kind != ""
	if wraps {
		className := node.kind
		if !node.sublanguage {
			className = "hljs-" + className
		}
		buffer.WriteString(`<span class="`)
		buffer.WriteString(className)
		buffer.WriteString(`">`)
	}
	for _, child := range node.children {
		if child.node != nil {
			walkHTML(buffer, child.node)
			continue
		}
		buffer.WriteString(escapeHTML(child.text))
	}
	if wraps {
		buffer.WriteString("</span>")
	}
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")

// escapeHTML is highlight.js escapeHTML.
func escapeHTML(value string) string { return htmlEscaper.Replace(value) }
