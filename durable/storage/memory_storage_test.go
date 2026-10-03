package storage_test

// Ports packages/durable/test/memory-storage.test.ts

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func TestMemoryStorageConformance(t *testing.T) {
	durabletest.RegisterStorageConformance(t, "MemoryStorage", func(use func(durable.Storage) error) error {
		return use(storage.NewMemoryStorage())
	})
}

// Upstream also expects mutating the exposed write to throw because it is frozen. Go has no frozen values, so the
// exposed writes are a detached copy instead; the case checks that mutating them cannot reach retained state.
func TestMemoryStorageDoesNotExposeRetainedStateThroughAPreparedCommit(t *testing.T) {
	ctx := context.Background()
	memory := storage.NewMemoryStorage()
	if _, err := memory.Commit(ctx, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
	}); err != nil {
		t.Fatal(err)
	}
	entryId := durable.IdFromNumber[durable.EntryId](2)
	prepared, err := memory.PrepareCommit([]durable.StorageWrite{durable.EntryWrite{Value: durable.EntryRecord{
		Id:             entryId,
		ConversationId: durable.ROOT_CONVERSATION_ID,
		Kind:           "test",
		Data:           map[string]any{"nested": []any{1}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	exposed, ok := prepared.Writes()[0].(durable.EntryWrite)
	if !ok {
		t.Fatal("Expected an entry write")
	}
	nested := exposed.Value.Data.(map[string]any)["nested"].([]any)
	nested[0] = 2
	exposed.Value.Data.(map[string]any)["nested"] = append(nested, 2)

	if seq := prepared.Apply(); seq != durable.SeqFromNumber(2) {
		t.Fatalf("apply = %d, want 2", seq)
	}
	if seq := prepared.Apply(); seq != durable.SeqFromNumber(2) {
		t.Fatalf("second apply = %d, want 2", seq)
	}
	found, err := memory.Entry(ctx, entryId)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"nested": []any{1}}; found == nil || !reflect.DeepEqual(found.Entry.Data, want) {
		t.Fatalf("entry data = %#v, want %#v", found, want)
	}
}
