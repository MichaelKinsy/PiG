package ai

import (
	"context"
	"testing"
)

// models-store.ts InMemoryModelsStore keeps one catalog per provider id: a delete removes only its provider's catalog, and a call whose
// abort signal already fired changes nothing.
func TestInMemoryModelsStoreKeepsOneCatalogPerProviderAndIgnoresCancelledWrites(t *testing.T) {
	newStore := NewInMemoryModelsStore
	store := newStore()
	ctx := context.Background()
	for _, provider := range []string{"radius", "other"} {
		if err := store.Write(ctx, provider, ModelsStoreEntry{ETag: `"` + provider + `"`}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Delete(ctx, "radius"); err != nil {
		t.Fatal(err)
	}
	if kept, err := store.Read(ctx, "other"); err != nil || kept == nil || kept.ETag != `"other"` {
		t.Fatalf("Delete(radius) removed another provider's catalog: %+v, %v", kept, err)
	}
	if gone, err := store.Read(ctx, "radius"); err != nil || gone != nil {
		t.Fatalf("Read(deleted) = %+v, %v; want nil, nil", gone, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.Delete(cancelled, "other"); err == nil {
		t.Fatal("Delete with a cancelled context succeeded")
	}
	if kept, err := store.Read(ctx, "other"); err != nil || kept == nil {
		t.Fatalf("a cancelled Delete removed the catalog: %+v, %v", kept, err)
	}
	if err := store.Write(cancelled, "third", ModelsStoreEntry{ETag: `"t"`}); err == nil {
		t.Fatal("Write with a cancelled context succeeded")
	}
	if third, err := store.Read(ctx, "third"); err != nil || third != nil {
		t.Fatalf("a cancelled Write stored a catalog: %+v, %v", third, err)
	}
}
