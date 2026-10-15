package codingagent

import (
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type warmTreeNode struct {
	id    string
	reads *atomic.Int64
}

func (n *warmTreeNode) NodeID() string               { return n.id }
func (n *warmTreeNode) NodeLabel() string            { return n.id }
func (n *warmTreeNode) NodeChildren() []tui.TreeNode { return nil }
func (n *warmTreeNode) NodeSearchableText() string {
	n.reads.Add(1)
	return "text of " + n.id
}

// handoffs/P1-real-session.md proposal 6: the /tree selector the editor slot shows reads every row's searchable text after its first paint and
// before it reads input, so the first search keystroke is a scan. The first chunk the loop receives here is a key release, which it drops, so the
// reads counted when it arrives all happened at open.
// mutation-checked: removing the WarmSearchTexts call from runEditorSlotTreeSelector leaves the count at 0 when the first chunk arrives.
func TestEditorSlotTreeSelectorBuildsSearchTextsBeforeReadingInput(t *testing.T) {
	input := make(chan []byte)
	m := &InteractiveMode{editor: tui.NewEditor(), editorContainer: tui.NewContainer(), layout: tui.NewContainer(), modalInputCh: input}
	m.tuiInst = tui.NewWithOutput(io.Discard, 120, 40)
	m.tuiInst.SetFocus(m.editor)
	var reads atomic.Int64
	var rows []tui.TreeNode
	for i := range 50 {
		rows = append(rows, &warmTreeNode{id: fmt.Sprintf("n%d", i), reads: &reads})
	}
	ts := tui.NewTreeSelectWithInitialFilter("t", warmTreeRoots(rows), "all")

	done := make(chan bool)
	go func() { _, ok := m.runEditorSlotTreeSelector(ts); done <- ok }()
	input <- []byte("\x1b[1;1:3A")
	if got := reads.Load(); got != 50 {
		t.Fatalf("searchable texts read before the first input chunk = %d, want one per row (50)", got)
	}
	input <- []byte("\x1b") // no search is active, so escape closes the selector
	<-done
	if got := reads.Load(); got != 50 {
		t.Fatalf("searchable texts read in total = %d, want each row's text read once (50)", got)
	}
}

// warmTreeRoots is the virtual root over the generated rows.
type warmTreeRoots []tui.TreeNode

func (warmTreeRoots) NodeID() string                 { return "" }
func (warmTreeRoots) NodeLabel() string              { return "" }
func (r warmTreeRoots) NodeChildren() []tui.TreeNode { return r }
