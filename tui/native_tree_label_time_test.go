package tui

import (
	"strings"
	"testing"
	"time"
)

type nselStampedNode struct {
	nselTreeNode
	stamp string
}

func (n *nselStampedNode) NodeChildren() []TreeNode   { return n.kids }
func (n *nselStampedNode) NodeLabelTimestamp() string { return n.stamp }

// tree-selector.ts:745-754: with label times on (the T key), a labeled row shows its label time before its entry text; the dock's rows carry the same text, a row without a label time
// shows none, and the label time stays off until it is toggled.
func TestNativeTreeSelectRowsCarryTheLabelTimeOnlyWhenToggledOn(t *testing.T) {
	treeHelpTestKeybindings(t, nil)
	stamp := time.Date(2020, 3, 5, 14, 7, 0, 0, time.Local).UTC().Format(time.RFC3339Nano)
	root := &nselTreeNode{kids: []TreeNode{
		&nselStampedNode{nselTreeNode: nselTreeNode{id: "a", label: "alpha", branch: "keep"}, stamp: stamp},
		&nselStampedNode{nselTreeNode: nselTreeNode{id: "b", label: "beta"}},
	}}
	ts := NewTreeSelectorComponent("", root)
	label := func(id string) string {
		for _, item := range nselNode(t, ts).Items {
			if item.ID == id {
				return item.Label
			}
		}
		t.Fatalf("no row %q", id)
		return ""
	}
	if got := label("a"); got != "[keep] alpha" {
		t.Fatalf("label time off: row = %q, want [keep] alpha", got)
	}
	ts.HandleInput("T")
	want := formatLabelTimestamp(stamp, time.Now())
	if got := label("a"); !strings.HasSuffix(got, " alpha") || !strings.Contains(got, want+" ") || !strings.HasPrefix(got, "[keep] ") {
		t.Errorf("label time on: row = %q, want [keep] %s alpha", got, want)
	}
	if got := label("b"); got != "beta" {
		t.Errorf("a row without a label time = %q, want beta", got)
	}
}
