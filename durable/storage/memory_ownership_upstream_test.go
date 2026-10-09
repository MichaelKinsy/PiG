package storage_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// MemoryStorage is a "detached in-memory reference implementation": "Reads and retained writes are cloned intentionally to match the
// ownership boundary of serialization-backed stores" (packages/durable/src/storage/memory.ts:229-233; clone at :270 and on reads). A value
// the caller passed to commit and a value a read returned are both the caller's to edit without reaching what the storage retains.
// mutation-checked: retaining the committed value without a copy, or returning the retained value from Entry, VisibleEntry or ScanEntries, fails it
func TestMemoryStorageClonesRetainedWritesAndReads(t *testing.T) {
	ctx := t.Context()
	memory := storage.NewMemoryStorage()
	if _, err := memory.Commit(ctx, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}); err != nil {
		t.Fatal(err)
	}
	entryID := durable.IdFromNumber[durable.EntryId](2)
	data := map[string]any{"nested": []any{1}}
	if _, err := memory.Commit(ctx, []durable.StorageWrite{durable.EntryWrite{Value: durable.EntryRecord{
		Id: entryID, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "test", Data: data,
	}}}); err != nil {
		t.Fatal(err)
	}
	data["nested"].([]any)[0] = 99 // edit the value passed to commit

	first, err := memory.Entry(ctx, entryID)
	if err != nil || first == nil {
		t.Fatalf("entry = %+v, %v", first, err)
	}
	want := map[string]any{"nested": []any{1}}
	if !reflect.DeepEqual(first.Entry.Data, want) {
		t.Fatalf("retained data = %#v, want %#v (the commit input was edited after commit)", first.Entry.Data, want)
	}
	first.Entry.Data.(map[string]any)["nested"].([]any)[0] = 77 // edit a value a read returned

	// Every read path returns a copy: the visibility lookup and the entry scan are edited the same way and the next read is unchanged.
	visible, err := memory.VisibleEntry(ctx, durable.ROOT_CONVERSATION_ID, entryID)
	if err != nil || visible == nil || !reflect.DeepEqual(visible.Entry.Data, want) {
		t.Fatalf("visible entry = %+v, %v; want %#v", visible, err, want)
	}
	visible.Entry.Data.(map[string]any)["nested"].([]any)[0] = 55
	page, err := memory.ScanEntries(ctx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 10, durable.Cursor{})
	if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0].Data, want) {
		t.Fatalf("scanned entries = %+v, %v; want one with %#v (the visible lookup's value was edited)", page, err, want)
	}
	page.Items[0].Data.(map[string]any)["nested"].([]any)[0] = 33

	second, err := memory.Entry(ctx, entryID)
	if err != nil || second == nil || !reflect.DeepEqual(second.Entry.Data, want) {
		t.Fatalf("second read = %+v, %v; want %#v (the first read's value was edited)", second, err, want)
	}
}

// The same ownership boundary for a conversation record (memory.ts:229-233): the Parent a read returned belongs to the caller, so editing it
// does not change what a later read returns.
// mutation-checked: returning the retained conversation without cloning its Parent fails it
func TestMemoryStorageConversationReadsAreCopies(t *testing.T) {
	ctx := t.Context()
	memory := storage.NewMemoryStorage()
	child := durable.IdFromNumber[durable.ConversationId](5)
	if _, err := memory.Commit(ctx, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: child, Parent: &durable.ConversationParent{ConversationId: durable.ROOT_CONVERSATION_ID, At: durable.IdFromNumber[durable.EntryId](2)}}},
	}); err != nil {
		t.Fatal(err)
	}
	first, err := memory.Conversation(ctx, child)
	if err != nil || first == nil || first.Parent == nil {
		t.Fatalf("conversation = %+v, %v", first, err)
	}
	first.Parent.At = durable.IdFromNumber[durable.EntryId](99) // edit a value a read returned

	second, err := memory.Conversation(ctx, child)
	if err != nil || second == nil || second.Parent == nil || second.Parent.At != durable.IdFromNumber[durable.EntryId](2) {
		t.Fatalf("second read = %+v, %v; want the Parent at entry 2 (the first read's value was edited)", second, err)
	}
}
