package codingagent

import (
	"encoding/json"
	"slices"
	"testing"
)

// session-manager.ts ContextEditEntry is { type: "context_edit", id, parentId: string | null, timestamp, targetId, replacement: { content } | null }. ContextEditEntry decodes and encodes exactly that wire shape, including parentId null and the null replacement.
// Pi: packages/coding-agent/src/core/session-manager.ts:177 (ContextEditEntry.targetId).
func TestContextEditEntryDecodesAndEncodesThePiWireShape(t *testing.T) {
	for _, wire := range []string{
		`{"type":"context_edit","id":"e1","parentId":null,"timestamp":"2025-01-01T00:00:00.000Z","targetId":"t1","replacement":null}`,
		`{"type":"context_edit","id":"e2","parentId":"e1","timestamp":"2025-01-01T00:00:01.000Z","targetId":"t1","replacement":{"content":"<keep & literal>"}}`,
		`{"type":"context_edit","id":"e3","parentId":"e2","timestamp":"2025-01-01T00:00:02.000Z","targetId":"t2","replacement":{"content":[{"type":"text","text":"x"}]}}`,
	} {
		var entry ContextEditEntry
		if err := json.Unmarshal([]byte(wire), &entry); err != nil {
			t.Fatalf("decode %s: %v", wire, err)
		}
		if entry.Type != "context_edit" || entry.ID == "" || entry.Timestamp == "" || entry.TargetID == "" {
			t.Errorf("decoded %+v from %s", entry, wire)
		}
		if wantNilParent := entry.ID == "e1"; wantNilParent != (entry.ParentID == nil) {
			t.Errorf("%s: ParentID = %v", entry.ID, entry.ParentID)
		}
		if wantNilReplacement := entry.ID == "e1"; wantNilReplacement != (entry.Replacement == nil) {
			t.Errorf("%s: Replacement = %+v", entry.ID, entry.Replacement)
		}
		got, err := marshalSessionLine(entry)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != wire {
			t.Errorf("encoded\n got %s\nwant %s", got, wire)
		}
	}
}

// session-manager.ts ProjectedSessionEntry pairs each raw branch entry (sourceEntry) with the model-visible messages it contributes after context edits: a message entry contributes its message, an omitted target and a state-only entry contribute none, and a context_edit contributes none itself.
func TestProjectedSessionEntryPairsSourceEntriesWithTheirMessages(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	userID := mustAppendContextMessage(t, sess, contextEditUser("request"))
	assistantID := mustAppendContextMessage(t, sess, contextEditAssistant("partial"))
	if _, err := sess.AppendThinkingLevelChange("high"); err != nil {
		t.Fatal(err)
	}
	editID := mustEdit(t, sess, assistantID, nil)

	projection := sess.BuildSessionProjection()
	type row struct {
		id       string
		messages int
	}
	var got []row
	for _, entry := range projection.Entries {
		got = append(got, row{entry.SourceEntry.Base().ID, len(entry.Messages)})
	}
	if len(got) != 4 || got[0] != (row{userID, 1}) || got[1] != (row{assistantID, 0}) || got[2].messages != 0 || got[3] != (row{editID, 0}) {
		t.Fatalf("projected entries = %+v, want user(1) assistant(omitted 0) thinking_level_change(0) edit(0)", got)
	}
	if want := []string{"user"}; !slices.Equal(projectedRoles(projection.Messages), want) {
		t.Fatalf("messages = %v, want %v", projectedRoles(projection.Messages), want)
	}
}
