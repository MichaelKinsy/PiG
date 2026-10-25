package storage_test

// pi: packages/durable/src/storage/memory.ts

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi storage/memory.ts:471-492, types.ts:1036: the visible lookup returns the entry with the commitSeq of the commit that wrote it,
// and a clone, so a caller that edits the result does not change what the storage holds.
func TestMemoryStorageVisibleEntryReturnsItsCommitSeqAndACopy(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStorage()
	mint := func() int64 {
		id, err := store.MintId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	root := durable.ROOT_CONVERSATION_ID
	first := durable.IdFromNumber[durable.EntryId](mint())
	second := durable.IdFromNumber[durable.EntryId](mint())
	write := func(id durable.EntryId) durable.StorageWrite {
		return durable.EntryWrite{Value: durable.EntryRecord{Id: id, ConversationId: root, Kind: "plain", Data: map[string]any{"n": int64(id)}}}
	}
	firstSeq, err := store.Commit(ctx, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: root}}, write(first)}, func(durable.Seq) {})
	if err != nil {
		t.Fatal(err)
	}
	secondSeq, err := store.Commit(ctx, []durable.StorageWrite{write(second)}, func(durable.Seq) {})
	if err != nil {
		t.Fatal(err)
	}
	if firstSeq == secondSeq {
		t.Fatalf("commit seqs %d and %d must differ", firstSeq, secondSeq)
	}
	for _, tc := range []struct {
		id   durable.EntryId
		want durable.Seq
	}{{first, firstSeq}, {second, secondSeq}} {
		got, err := store.VisibleEntry(ctx, root, tc.id)
		if err != nil || got == nil {
			t.Fatalf("VisibleEntry(%d) = %+v, %v", tc.id, got, err)
		}
		if got.CommitSeq != tc.want {
			t.Errorf("VisibleEntry(%d).CommitSeq = %d, want %d", tc.id, got.CommitSeq, tc.want)
		}
	}

	got, err := store.VisibleEntry(ctx, root, first)
	if err != nil || got == nil {
		t.Fatalf("VisibleEntry = %+v, %v", got, err)
	}
	got.Entry.Data.(map[string]any)["n"] = int64(-1)
	again, err := store.VisibleEntry(ctx, root, first)
	if err != nil || again == nil {
		t.Fatalf("VisibleEntry = %+v, %v", again, err)
	}
	if n := again.Entry.Data.(map[string]any)["n"]; n != int64(first) {
		t.Errorf("an edit of the returned entry reached the stored one: n = %v, want %d", n, first)
	}
}
