package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type treeTestNode struct {
	id   string
	kids []tui.TreeNode
}

func (n *treeTestNode) NodeID() string               { return n.id }
func (n *treeTestNode) NodeLabel() string            { return n.id }
func (n *treeTestNode) NodeChildren() []tui.TreeNode { return n.kids }

func newTestTree() *tui.TreeSelect {
	root := &treeTestNode{id: "root", kids: []tui.TreeNode{
		&treeTestNode{id: "a"}, &treeTestNode{id: "b"}, &treeTestNode{id: "c"},
	}}
	return tui.NewTreeSelect("", root)
}

// TestModalSelectorDropsKittyKeyRelease pins the contract that chunks produced
// by StdinBuffer for a focused modal component carry key presses only. The
// Kitty keyboard protocol (pig pushes \x1b[>7u, whose flag 2 reports
// press/repeat/release) sends a release for every press, so a modal that
// dispatches raw chunks moves its cursor twice per keystroke.
//
// This is the exact pipeline used by runEditorSlotTreeSelector (/tree),
// runEditorSlotUserMessageSelector and RunRemoteOverlay: read bytes ->
// StdinBuffer.ProcessBytes -> component.HandleInput.
//
// Mirrors upstream tui.ts:887 (isKeyRelease(data) && !wantsKeyRelease -> drop).
func TestModalSelectorDropsKittyKeyRelease(t *testing.T) {
	// NewTreeSelect opens with the cursor on the last row ("c"), so a single
	// Up must land on "b"; a dispatched release moves twice, to "a".
	tests := []struct {
		name  string
		input string
		want  string // SelectedID after one Up then Enter
	}{
		// Kitty Up: press (event type :1) immediately followed by release (:3).
		{"kitty up press+release", "\x1b[1;1:1A\x1b[1;1:3A", "b"},
		// Kitty with no explicit event type on press, release still tagged :3.
		{"kitty up bare press + release", "\x1b[1;1A\x1b[1;1:3A", "b"},
		// Legacy terminals send no release at all; must still move exactly once.
		{"legacy up", "\x1b[A", "b"},
		// SS3 form (application cursor keys).
		{"ss3 up", "\x1bOA", "b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestTree()
			var buf StdinBuffer
			dispatchModalInput(ts, buf.ProcessBytes([]byte(tc.input)), ts.HandleInput, ts.Done)
			dispatchModalInput(ts, buf.ProcessBytes([]byte("\r")), ts.HandleInput, ts.Done)
			if !ts.Done() {
				t.Fatalf("tree not done after Enter")
			}
			if got := ts.SelectedID(); got != tc.want {
				t.Errorf("SelectedID = %q; want %q (double-move means the release was dispatched)", got, tc.want)
			}
		})
	}
}
