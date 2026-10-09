package durable

import "testing"

// Pi packages/durable/src/types.ts:705 DocumentCommitChange = a document change or a document copy; CommitChange (types.ts:732) adds the table changes. Each variant reports its upstream type discriminator.
func TestDocumentCommitChangeVariantsCarryTheirDiscriminators(t *testing.T) {
	var documents = []DocumentCommitChange{DocumentChange{}, DocumentCopyChange{}}
	want := []string{"document", "document.copy"}
	for index, change := range documents {
		if got := CommitChangeType(change); got != want[index] {
			t.Errorf("CommitChangeType(%T) = %q, want %q", change, got, want[index])
		}
	}
	var tables = []CommitChange{ConversationWrite{}, EntryWrite{}, TaskWrite{}, SubmissionWrite{}}
	for index, want := range []string{"conversation", "entry", "task", "submission"} {
		if got := CommitChangeType(tables[index]); got != want {
			t.Errorf("CommitChangeType(%T) = %q, want %q", tables[index], got, want)
		}
		if _, ok := tables[index].(DocumentCommitChange); ok {
			t.Errorf("%T must not be a DocumentCommitChange", tables[index])
		}
	}
}
