package ai

import (
	"context"
	"errors"
	"testing"
)

// models-store.ts InMemoryModelsStore: read is undefined for an unknown provider, write and read hand out structured clones (a caller changing either side
// does not reach the store), delete removes only its provider, and an aborted signal makes each operation throw before it acts.
func TestInMemoryModelsStoreKeepsDetachedCopiesPerProvider(t *testing.T) {
	store := NewInMemoryModelsStore()
	if _, ok := ModelsStore(store).(*InMemoryModelsStore); !ok {
		t.Fatalf("NewInMemoryModelsStore returned %T", store)
	}
	ctx := context.Background()
	if entry, err := store.Read(ctx, "missing"); entry != nil || err != nil {
		t.Fatalf("Read of an unknown provider = %v, %v; want nil, nil", entry, err)
	}
	checked := 10.0
	written := ModelsStoreEntry{Models: []AnyModel{}, CheckedAt: &checked, ETag: "a"}
	if err := store.Write(ctx, "one", written); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, "two", ModelsStoreEntry{Models: []AnyModel{}, ETag: "b"}); err != nil {
		t.Fatal(err)
	}
	checked = 99
	first, err := store.Read(ctx, "one")
	if err != nil || first == nil || first.ETag != "a" || first.CheckedAt == nil || *first.CheckedAt != 10 {
		t.Fatalf("Read after the caller changed its entry = %+v, %v; want the written copy", first, err)
	}
	*first.CheckedAt = 55
	second, err := store.Read(ctx, "one")
	if err != nil || second == nil || *second.CheckedAt != 10 {
		t.Fatalf("Read after the reader changed its copy = %+v, %v; want the stored value", second, err)
	}
	if err := store.Delete(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if entry, _ := store.Read(ctx, "one"); entry != nil {
		t.Fatalf("Read after Delete = %+v", entry)
	}
	if entry, _ := store.Read(ctx, "two"); entry == nil || entry.ETag != "b" {
		t.Fatalf("Delete of one provider removed the other: %+v", entry)
	}
}

func TestInMemoryModelsStoreAbortedContextFailsBeforeActing(t *testing.T) {
	store := NewInMemoryModelsStore()
	live := context.Background()
	if err := store.Write(live, "p", ModelsStoreEntry{Models: []AnyModel{}, ETag: "kept"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(live)
	cancel()
	if _, err := store.Read(ctx, "p"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read with an aborted signal = %v", err)
	}
	if err := store.Write(ctx, "q", ModelsStoreEntry{Models: []AnyModel{}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write with an aborted signal = %v", err)
	}
	if err := store.Delete(ctx, "p"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete with an aborted signal = %v", err)
	}
	if entry, _ := store.Read(live, "q"); entry != nil {
		t.Fatal("an aborted Write stored its entry")
	}
	if entry, _ := store.Read(live, "p"); entry == nil || entry.ETag != "kept" {
		t.Fatalf("an aborted Delete removed the entry: %+v", entry)
	}
}

// The tests of this file drive the in-memory store type by name.
