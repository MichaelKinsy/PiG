// SPDX-License-Identifier: MIT

package frontend

import "testing"

// The node set is closed: a frontend handles each kind by a type switch, and
// the editor, a selector or settings in its place, the working indicator,
// the footer and an overlay are kinds a dock or overlay op carries.
func TestNodeKindsAreClosed(t *testing.T) {
	nodes := []Node{Lines{}, ToolCard{}, MarkdownText{}, Thinking{}, Editor{}, Selector{}, Settings{}, Working{}, Footer{}, Overlay{}}
	seen := map[string]bool{}
	for _, node := range nodes {
		var kind string
		switch node.(type) {
		case Lines:
			kind = "lines"
		case ToolCard:
			kind = "tool"
		case MarkdownText:
			kind = "markdown"
		case Thinking:
			kind = "thinking"
		case Editor:
			kind = "editor"
		case Working:
			kind = "working"
		case Footer:
			kind = "footer"
		case Selector:
			kind = "selector"
		case Settings:
			kind = "settings"
		case Overlay:
			kind = "overlay"
		}
		if kind == "" || seen[kind] {
			t.Fatalf("node %T has no kind of its own", node)
		}
		seen[kind] = true
	}
}
