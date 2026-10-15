package tui

import (
	"fmt"
	"strings"
	"testing"
)

type editableTreeTestNode struct {
	fakeNodeWithBranchLabel
	timestamp string
}

func (n *editableTreeTestNode) NodeLabelTimestamp() string { return n.timestamp }
func (n *editableTreeTestNode) NodeFilterTags() []string {
	if n.branchLabel != "" {
		return []string{"labeled"}
	}
	return nil
}

func (n *editableTreeTestNode) SetNodeBranchLabel(label, timestamp string) {
	n.branchLabel, n.timestamp = label, timestamp
}

// Pi LabelInput (tree-selector.ts:1278-1335) uses the shared Input, starts with the branch label, trims on save, and updates tree state before notifying its caller.
func TestTreeLabelInputContract(t *testing.T) {
	for _, tc := range []struct {
		name, initial, want string
		keys                []string
		bindings            map[string][]string
	}{
		{"unlabeled entry stays empty", "", "", nil, nil},
		{"prefill retains Input start cursor", "checkpoint", "Xcheckpoint", []string{"X"}, nil},
		{"cursor movement inserts in the middle", "AB", "AxB", []string{"\x1b[C", "x"}, nil},
		{"bracketed paste spans chunks", "AB", "pastedAB", []string{"\x1b[200~pasted", "\x1b[201~"}, nil},
		{"grapheme backspace", "😀X", "", []string{"\x05", "\x7f", "\x7f"}, nil},
		{"configured deletion", "text", "tex", []string{"\x05", "\x07"}, map[string][]string{KBEditorDeleteCharBack: {"ctrl+g"}}},
		{"JavaScript save trim", "\ufeff spaced \u00a0", "spaced", nil, nil},
		{"replace historical ANSI label", "\x1b[31mold label\x1b[39m", "clean", []string{"\x0b", "clean"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			treeHelpTestKeybindings(t, tc.bindings)
			node := &editableTreeTestNode{fakeNodeWithBranchLabel: fakeNodeWithBranchLabel{fakeNode: fakeNode{id: "entry", label: "unique message body"}, branchLabel: tc.initial}}
			selector := NewTreeSelectorComponent("", &fakeNode{id: "root", kids: []TreeNode{node}})
			calls := 0
			selector.OnLabelEdit = func(id, label string) {
				calls++
				if id != "entry" || label != tc.want {
					t.Errorf("saved %q/%q, want entry/%q", id, label, tc.want)
				}
				if node.branchLabel != label {
					t.Errorf("caller observed stale label %q instead of %q", node.branchLabel, label)
				}
				if (label == "") != (node.timestamp == "") {
					t.Errorf("label/timestamp presence differs: %q/%q", label, node.timestamp)
				}
			}
			selector.HandleInput("L")
			for _, key := range tc.keys {
				selector.HandleInput(key)
			}
			lines := strings.Join(selector.Render(80), "\n")
			if !strings.Contains(lines, "Label (empty to remove):") || strings.Contains(lines, "unique message body") {
				t.Errorf("label editor did not replace tree rows: %q", lines)
			}
			selector.HandleInput("\r")
			if calls != 1 || selector.Done() {
				t.Fatalf("save calls=%d done=%v", calls, selector.Done())
			}
			if tc.want != "" && !strings.Contains(strings.Join(selector.Render(80), "\n"), "["+tc.want+"]") {
				t.Fatal("saved label did not appear in the retained tree")
			}
			selector.HandleInput("\x0c")
			if (len(selector.rows) == 0) != (tc.want == "") {
				t.Fatal("labeled filter retained pre-edit metadata")
			}
		})
	}
}

func BenchmarkTreeLabelEdit(b *testing.B) {
	for _, size := range []int{100, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			old := GetTUIKeybindings()
			b.Cleanup(func() { SetTUIKeybindings(old) })
			defs := TUIKeybindingDefinitionsFor(HostKeybindingPlatform())
			defs["app.tree.editLabel"] = TUIKeybindingDef{DefaultKeys: []string{"shift+l"}}
			SetTUIKeybindings(NewKeybindingsManager(defs, nil))
			root := &fakeNode{id: "root"}
			for i := range size {
				root.kids = append(root.kids, &editableTreeTestNode{fakeNodeWithBranchLabel: fakeNodeWithBranchLabel{fakeNode: fakeNode{id: fmt.Sprint(i), label: "message"}}})
			}
			selector := NewTreeSelectorComponent("", root)
			selector.SetInitialCursor("0", "")
			b.ReportAllocs()
			for b.Loop() {
				selector.HandleInput("L")
				selector.HandleInput("\x0b")
				selector.HandleInput("x")
				selector.Render(120)
				selector.HandleInput("\r")
			}
		})
	}
}

func TestTreeLabelCancelKeepsBranchLabel(t *testing.T) {
	treeHelpTestKeybindings(t, nil)
	node := &editableTreeTestNode{fakeNodeWithBranchLabel: fakeNodeWithBranchLabel{fakeNode: fakeNode{id: "entry", label: "body"}, branchLabel: "original"}}
	selector := NewTreeSelectorComponent("", &fakeNode{id: "root", kids: []TreeNode{node}})
	selector.OnLabelEdit = func(string, string) { t.Fatal("cancel saved a label") }
	selector.HandleInput("L")
	selector.HandleInput("changed")
	selector.HandleInput("\x1b")
	if node.branchLabel != "original" || selector.Done() {
		t.Fatalf("cancel label=%q done=%v", node.branchLabel, selector.Done())
	}
}
