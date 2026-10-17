package codingagent

import (
	"slices"
	"testing"
)

// session-manager.ts:556-571 buildSessionProjection: each ProjectedSessionEntry owns its raw source entry and only its own model-visible messages. State-only entries (context_edit) project to no messages, an omitted target projects to none, a replaced target keeps its message with the new content, and `messages` is the flattened concatenation of the entries' messages.
func TestSessionProjectionAttributesMessagesToEachSourceEntry(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	userID := mustAppendContextMessage(t, sess, contextEditUser("request"))
	omittedID := mustAppendContextMessage(t, sess, contextEditAssistant("omitted"))
	replacedID := mustAppendContextMessage(t, sess, contextEditAssistant("original"))
	keptID := mustAppendContextMessage(t, sess, contextEditAssistant("kept"))
	omitEditID := mustEdit(t, sess, omittedID, nil)
	replaceEditID := mustEdit(t, sess, replacedID, replacementBlocks(t, "replaced"))

	projection := sess.BuildSessionProjection()
	type row struct {
		id    string
		typ   string
		texts []string
	}
	var got []row
	for _, entry := range projection.Entries {
		got = append(got, row{entry.SourceEntry.Base().ID, entry.SourceEntry.Base().Type, projectedTexts(entry.Messages)})
	}
	want := []row{
		{userID, "message", []string{"request"}},
		{omittedID, "message", []string{}},
		{replacedID, "message", []string{"replaced"}},
		{keptID, "message", []string{"kept"}},
		{omitEditID, "context_edit", []string{}},
		{replaceEditID, "context_edit", []string{}},
	}
	if len(got) != len(want) {
		t.Fatalf("projected %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].id != want[i].id || got[i].typ != want[i].typ || !slices.Equal(got[i].texts, want[i].texts) {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	var flattened []string
	for _, entry := range projection.Entries {
		flattened = append(flattened, projectedTexts(entry.Messages)...)
	}
	if !slices.Equal(projectedTexts(projection.Messages), flattened) {
		t.Errorf("Messages = %q, want the entries' messages concatenated %q", projectedTexts(projection.Messages), flattened)
	}
}

// A compaction contributes its summary only as the first projected entry; the entries it kept follow it with their own messages (session-manager.ts:552-566).
func TestSessionProjectionAttributesTheCompactionSummaryToTheCompactionEntry(t *testing.T) {
	sess := NewSession("s", t.TempDir())
	mustAppendContextMessage(t, sess, contextEditUser("old request"))
	keptID := mustAppendContextMessage(t, sess, contextEditAssistant("kept answer"))
	compactionID := mustCompact(t, sess, "the summary", keptID, 100)
	afterID := mustAppendContextMessage(t, sess, contextEditUser("new request"))

	entries := sess.BuildSessionProjection().Entries
	var ids []string
	for _, entry := range entries {
		ids = append(ids, entry.SourceEntry.Base().ID)
	}
	if !slices.Equal(ids, []string{compactionID, keptID, afterID}) {
		t.Fatalf("projected entries %v, want compaction, kept entry, later entry", ids)
	}
	if got := projectedTexts(entries[0].Messages); len(got) != 1 || got[0] != "the summary" {
		t.Errorf("compaction entry messages = %q, want its summary", got)
	}
	if got := projectedTexts(entries[1].Messages); len(got) != 1 || got[0] != "kept answer" {
		t.Errorf("kept entry messages = %q", got)
	}
	if got := projectedTexts(entries[2].Messages); len(got) != 1 || got[0] != "new request" {
		t.Errorf("later entry messages = %q", got)
	}
}
