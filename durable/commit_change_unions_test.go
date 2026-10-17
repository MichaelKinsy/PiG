package durable

import "testing"

// TestCommitChangeUnionMembers pins the `type` discriminator of each CommitChange member (types.ts): the four table
// writes share a discriminator with their storage write; a document change is "document" or "document.copy". The copy
// change carries the exact persisted source selected for the copy.
func TestCommitChangeUnionMembers(t *testing.T) {
	tables := map[string]TableCommitChange{
		"conversation": ConversationWrite{},
		"entry":        EntryWrite{},
		"task":         TaskWrite{},
		"submission":   SubmissionWrite{},
	}
	for want, change := range tables {
		if got := CommitChangeType(change); got != want {
			t.Errorf("%T type = %q, want %q", change, got, want)
		}
	}
	documents := map[string]DocumentCommitChange{
		"document":      DocumentChange{},
		"document.copy": DocumentCopyChange{Source: DocumentCopySource{Id: 12, At: AtSeq(3)}},
	}
	for want, change := range documents {
		if got := CommitChangeType(change); got != want {
			t.Errorf("%T type = %q, want %q", change, got, want)
		}
	}
	copied := documents["document.copy"].(DocumentCopyChange).Source
	if copied.Id != 12 || copied.At != AtSeq(3) {
		t.Errorf("copy source = %+v", copied)
	}
}
