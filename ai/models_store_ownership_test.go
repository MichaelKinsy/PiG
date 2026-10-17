package ai

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Pi's shared directory lease rejects a compromised owner before commit. The injected lock seam must retain that check, not merely report a release error after writing.
func TestFileModelsStoreChecksLeaseBeforeCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	store := NewFileModelsStore(path)
	if err := store.Write(t.Context(), "retained", ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{json.RawMessage(`{"id":"kept"}`)})}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = store.withLock(t.Context(), func(entries []storedModels) ([]storedModels, error) {
		stamp := time.Unix(1, 0)
		if err := os.Chtimes(path+".lock", stamp, stamp); err != nil {
			t.Fatal(err)
		}
		return append(entries, storedModels{providerID: "must-not-commit", raw: json.RawMessage(`{"models":[]}`)}), nil
	})
	if err == nil {
		t.Fatal("compromised lease accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("compromised callback changed storage: %s, %v", after, err)
	}
}
