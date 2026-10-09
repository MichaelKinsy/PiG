package storage_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// prepareCommit (packages/durable/src/storage/memory.ts:266-288) validates and detaches one commit "without changing observable state": nothing is
// readable and no sequence is consumed until apply() runs, a write that fails validation leaves the storage as it was, and a closed storage
// refuses to prepare (assertOpen, :862 "MemoryStorage is closed"). apply() then makes the writes readable at the prepared sequence.
// mutation-checked: applying the writes in PrepareCommit, advancing the sequence while preparing, skipping the closed check or the duplicate-id check fails it
func TestPrepareCommitChangesNothingUntilApplied(t *testing.T) {
	ctx := t.Context()
	memory := storage.NewMemoryStorage()
	rootWrite := durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}

	prepared, err := memory.PrepareCommit([]durable.StorageWrite{rootWrite})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := memory.Conversation(ctx, durable.ROOT_CONVERSATION_ID); err != nil || got != nil {
		t.Fatalf("before apply, the conversation = %+v, %v; want none", got, err)
	}
	again, err := memory.PrepareCommit(nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Seq() != prepared.Seq() {
		t.Fatalf("a second prepare got sequence %d, want the unconsumed %d", again.Seq(), prepared.Seq())
	}
	if seq := prepared.Apply(); seq != prepared.Seq() {
		t.Fatalf("apply = %d, want %d", seq, prepared.Seq())
	}
	if got, err := memory.Conversation(ctx, durable.ROOT_CONVERSATION_ID); err != nil || got == nil || got.Id != durable.ROOT_CONVERSATION_ID {
		t.Fatalf("after apply, the conversation = %+v, %v; want the root", got, err)
	}
	if next, err := memory.PrepareCommit(nil); err != nil || next.Seq() != prepared.Seq()+1 {
		t.Fatalf("the sequence after apply = %+v, %v; want %d", next, err, prepared.Seq()+1)
	}

	// A write that fails validation prepares nothing and changes nothing: the same conversation id cannot be created twice in one batch.
	if _, err := memory.PrepareCommit([]durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.IdFromNumber[durable.ConversationId](40)}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.IdFromNumber[durable.ConversationId](40)}},
	}); err == nil || err.Error() != "ID 40 is written more than once" {
		t.Fatalf("a batch that writes one conversation id twice = %v, want ID 40 is written more than once (memory.ts:744)", err)
	}
	if got, err := memory.Conversation(ctx, durable.IdFromNumber[durable.ConversationId](40)); err != nil || got != nil {
		t.Fatalf("a failed prepare left conversation 40 = %+v, %v", got, err)
	}

	if err := memory.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.PrepareCommit(nil); err == nil || err.Error() != "MemoryStorage is closed" {
		t.Fatalf("prepare on a closed storage = %v, want MemoryStorage is closed", err)
	}
}
