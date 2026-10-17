package storage_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi packages/durable/src/storage/memory.ts:207 PreparedMemoryCommit.seq is the sequence the mutation applies at; Apply returns that same sequence and a later prepare takes the next one.
func TestPreparedMemoryCommitSeqIsTheApplySequence(t *testing.T) {
	memory := storage.NewMemoryStorage()
	root := []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}
	first, err := memory.PrepareCommit(root)
	if err != nil {
		t.Fatal(err)
	}
	seq := first.Seq()
	if applied := first.Apply(); applied != seq {
		t.Fatalf("Apply = %d, want the prepared sequence %d", applied, seq)
	}
	second, err := memory.PrepareCommit(nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Seq() <= seq {
		t.Fatalf("second Seq = %d, want above %d", second.Seq(), seq)
	}
}

// Pi memory.ts:250-254 prepareCommit(writes, seq): an explicit sequence is the one the mutation applies at (the JSONL recovery replays each
// marker's own sequence, jsonl/storage.ts:707), and a sequence below the next one is rejected with "Commit sequence N does not strictly
// increase" (packages/durable/src/storage/memory.ts:252-254).
func TestPrepareCommitAtAppliesAtTheGivenSequenceAndRejectsAStaleOne(t *testing.T) {
	memory := storage.NewMemoryStorage()
	root := []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}
	next, err := memory.PrepareCommit(nil)
	if err != nil {
		t.Fatal(err)
	}
	at := next.Seq() + 4
	prepared, err := memory.PrepareCommitAt(root, at)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Seq() != at || prepared.Apply() != at {
		t.Fatalf("seq = %d, want the given %d", prepared.Seq(), at)
	}
	if _, err := memory.PrepareCommitAt(nil, at); err == nil || err.Error() != fmt.Sprintf("Commit sequence %d does not strictly increase", at) {
		t.Fatalf("a sequence that is not above the applied one must be rejected, got %v", err)
	}
}
