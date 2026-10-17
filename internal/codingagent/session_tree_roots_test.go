package codingagent

import (
	"encoding/json"
	"fmt"
	"testing"
)

func treeEntry(id string, parent any, timestamp string) json.RawMessage {
	parentJSON, _ := json.Marshal(parent)
	return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":%q,"message":{"role":"user","content":"x","timestamp":1}}`, id, parentJSON, timestamp))
}

// session-manager.ts:1529-1562 getTree: an entry with no parent, an entry that is its own parent and an orphan whose parent is missing are all roots, in entry order and unsorted; children are sorted oldest first by parsed time,: 03.000Z is older than 03.500Z although "Z" sorts after "." as text.
func TestGetTreeRootsAndChildOrderFollowUpstream(t *testing.T) {
	cwd := t.TempDir()
	header := json.RawMessage(fmt.Sprintf(`{"type":"session","version":3,"id":"tree","timestamp":"2025-01-01T00:00:00.000Z","cwd":%q}`, cwd))
	s, err := newSessionFromEntries(cwd, "tree", fileEntriesOf([]json.RawMessage{
		header,
		treeEntry("late-root", nil, "2025-01-01T00:00:09.000Z"),
		treeEntry("self", "self", "2025-01-01T00:00:01.000Z"),
		treeEntry("orphan", "missing", "2025-01-01T00:00:05.000Z"),
		treeEntry("whole", "late-root", "2025-01-01T00:00:03Z"),
		treeEntry("fraction", "late-root", "2025-01-01T00:00:03.500Z"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	roots := s.GetTree()
	var rootIDs []string
	for _, root := range roots {
		rootIDs = append(rootIDs, root.Entry.Base().ID)
	}
	if fmt.Sprint(rootIDs) != "[late-root self orphan]" {
		t.Fatalf("roots = %v, want entry order [late-root self orphan]", rootIDs)
	}
	var childIDs []string
	for _, child := range roots[0].Children {
		childIDs = append(childIDs, child.Entry.Base().ID)
	}
	if fmt.Sprint(childIDs) != "[whole fraction]" {
		t.Fatalf("children = %v, want oldest first by parsed time", childIDs)
	}
}
