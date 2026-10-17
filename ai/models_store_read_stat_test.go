package ai

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// Pi models-store.ts stats models-store.json on every read, as AuthStorage does auth.json. The stat runs before the read-state mutex, so a catalog restore's provider goroutines do not queue behind each other's stats.
func TestFileModelsStoreReadStatsOutsideTheReadLock(t *testing.T) {
	freshSharedModelsReadState(t)
	path := filepath.Join(t.TempDir(), "models-store.json")
	store := NewFileModelsStore(path)
	if err := store.Write(t.Context(), "one", ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{upstreamStoredModel("one", "m")})}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(t.Context(), "one"); err != nil {
		t.Fatal(err)
	}
	// The warm-up's reload goroutine publishes completion under the mutex; wait for it to release before watching.
	store.readState.mu.Lock()
	//lint:ignore SA2001 the empty critical section waits for the reload goroutine's unlock.
	store.readState.mu.Unlock() //nolint:staticcheck // SA2001: the empty section waits for the reload goroutine's unlock.
	stats := watchRevisionStats(t, store.path, func() { assertUnlocked(t, &store.readState.mu, "models-store.json") })
	for range 3 {
		if _, err := store.Read(t.Context(), "one"); err != nil {
			t.Fatal(err)
		}
	}
	if got := stats(); got != 3 {
		t.Fatalf("models-store.json stats = %d, want one per read (3)", got)
	}
}
