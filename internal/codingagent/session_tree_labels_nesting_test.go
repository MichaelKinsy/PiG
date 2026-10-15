package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func treeLabelEntry(id, parent, target string, label any, timestamp string) json.RawMessage {
	labelJSON, _ := json.Marshal(label)
	return json.RawMessage(fmt.Sprintf(`{"type":"label","id":%q,"parentId":%q,"timestamp":%q,"targetId":%q,"label":%s}`, id, parent, timestamp, target, labelJSON))
}

// session-manager.ts getTree: the latest label entry for a target decides its label and label timestamp (an empty or null label clears it), and every level of children is sorted oldest first, not only the roots' children.
// Pi: packages/coding-agent/src/core/session-manager.ts getTree.
func TestGetTreeResolvesLabelsAndSortsEveryLevelOfChildren(t *testing.T) {
	cwd := t.TempDir()
	header := json.RawMessage(fmt.Sprintf(`{"type":"session","version":3,"id":"tree","timestamp":"2025-01-01T00:00:00.000Z","cwd":%q}`, cwd))
	s, err := newSessionFromEntries(cwd, "tree", fileEntriesOf([]json.RawMessage{
		header,
		treeEntry("root", nil, "2025-01-01T00:00:01.000Z"),
		treeEntry("child", "root", "2025-01-01T00:00:02.000Z"),
		treeEntry("grand-late", "child", "2025-01-01T00:00:09.000Z"),
		treeEntry("grand-early", "child", "2025-01-01T00:00:03.000Z"),
		treeLabelEntry("l1", "grand-late", "child", "first", "2025-01-01T00:00:10.000Z"),
		treeLabelEntry("l2", "l1", "child", "second", "2025-01-01T00:00:11.000Z"),
		treeLabelEntry("l3", "l2", "grand-late", "kept", "2025-01-01T00:00:12.000Z"),
		treeLabelEntry("l4", "l3", "grand-late", "", "2025-01-01T00:00:13.000Z"),
		treeLabelEntry("l5", "l4", "grand-early", "gone", "2025-01-01T00:00:14.000Z"),
		treeLabelEntry("l6", "l5", "grand-early", nil, "2025-01-01T00:00:15.000Z"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	roots := s.GetTree()
	byID := map[string]*SessionTreeNode{}
	var walk func(n *SessionTreeNode)
	walk = func(n *SessionTreeNode) {
		byID[n.Entry.Base().ID] = n
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	child := byID["child"]
	if child == nil || child.Label != "second" || child.LabelTimestamp != "2025-01-01T00:00:11.000Z" {
		t.Fatalf("child label = %+v, want the latest label entry's text and timestamp", child)
	}
	for _, id := range []string{"grand-late", "grand-early"} {
		if n := byID[id]; n == nil || n.Label != "" || n.LabelTimestamp != "" {
			t.Errorf("%s label = %q at %q, want it cleared by the later empty or null label", id, n.Label, n.LabelTimestamp)
		}
	}
	var order []string
	for _, c := range child.Children {
		order = append(order, c.Entry.Base().ID)
	}
	if got := strings.Join(order, ","); got != "grand-early,grand-late" {
		t.Errorf("grandchildren = %s, want oldest first at every depth", got)
	}
}

// session-manager.ts buildContextEntries returns an array: a session with no leaf yields an empty, non-nil list.
func TestBuildContextEntriesOfAnEmptySessionIsAnEmptyList(t *testing.T) {
	if got := NewSession("empty", t.TempDir()).BuildContextEntries(); got == nil || len(got) != 0 {
		t.Fatalf("BuildContextEntries() = %#v, want an empty non-nil list", got)
	}
}
