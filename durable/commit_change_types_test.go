package durable_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// packages/durable/src/types.ts:704-732: CommitChange is a TableCommitChange (a StorageWrite of type conversation, entry, task or submission) or a
// DocumentCommitChange (type "document", or "document.copy" for a definition-free copy); each variant's `type` discriminator is CommitChangeType.
func TestCommitChangeVariantsCarryPiDiscriminators(t *testing.T) {
	var tables = []durable.TableCommitChange{durable.ConversationWrite{}, durable.EntryWrite{}, durable.TaskWrite{}, durable.SubmissionWrite{}}
	wantTables := []string{"conversation", "entry", "task", "submission"}
	for i, change := range tables {
		if got := durable.CommitChangeType(change); got != wantTables[i] {
			t.Errorf("%T: type = %q, want %q", change, got, wantTables[i])
		}
	}
	documents := []durable.DocumentCommitChange{durable.DocumentChange{}, durable.DocumentCopyChange{}}
	wantDocuments := []string{"document", "document.copy"}
	for i, change := range documents {
		if got := durable.CommitChangeType(change); got != wantDocuments[i] {
			t.Errorf("%T: type = %q, want %q", change, got, wantDocuments[i])
		}
	}
}
