package storage_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi storage/memory.ts PreparedMemoryCommit.apply (:207-): preparing validates and detaches without changing what storage shows; apply performs the mutation once
// and every later call returns the same sequence without applying it again.
func TestPreparedMemoryCommitAppliesOnceAndStaysInvisibleUntilApplied(t *testing.T) {
	ctx := context.Background()
	memory := storage.NewMemoryStorage()
	entryID := durable.IdFromNumber[durable.EntryId](mustMint(t, memory))
	prepared, err := memory.PrepareCommit([]durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
		durable.EntryWrite{Value: durable.EntryRecord{Id: entryID, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "plain"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if found, err := memory.Entry(ctx, entryID); err != nil || found != nil {
		t.Fatalf("a prepared commit is visible before Apply: %+v, %v", found, err)
	}
	seq := prepared.Apply()
	if again := prepared.Apply(); again != seq {
		t.Fatalf("second Apply = %d, want the same sequence %d", again, seq)
	}
	page, err := memory.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 10, nil)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("after two Apply calls the conversation holds %d entries (%v), want 1", len(page.Items), err)
	}
	if next, err := memory.PrepareCommit(nil); err != nil || next.Seq() != seq+1 {
		t.Fatalf("the next prepared sequence = %v (%v), want %d", next, err, seq+1)
	}
}

// memory.ts prepareCommit(writes, seq): a sequence that is not a safe integer is rejected like one that does not increase.
func TestPrepareCommitAtRejectsASequenceBeyondTheSafeIntegers(t *testing.T) {
	memory := storage.NewMemoryStorage()
	const unsafe = durable.Seq(1 << 53)
	if _, err := memory.PrepareCommitAt(nil, unsafe); err == nil || err.Error() != fmt.Sprintf("Commit sequence %d does not strictly increase", int64(unsafe)) {
		t.Fatalf("PrepareCommitAt(2^53) = %v, want the strictly-increase error", err)
	}
	if prepared, err := memory.PrepareCommitAt(nil, durable.Seq(1<<53-1)); err != nil || prepared.Seq() != 1<<53-1 {
		t.Fatalf("PrepareCommitAt(2^53-1) = %v, %v, want that sequence accepted", prepared, err)
	}
}

func mustMint(t *testing.T, memory *storage.MemoryStorage) int64 {
	t.Helper()
	id, err := memory.MintId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
