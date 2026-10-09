package ai

// pi: packages/coding-agent/src/core/models-store.ts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// Pi models-store.ts:55 uses FileAuthStorageBackend, so dynamic catalogs
// must coordinate with the same directory-lock protocol as credentials.
func TestFileModelsStoreCancelledWriteDoesNotCommitLater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	original := `{"one":{"models":[{"id":"existing"}]}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := pilock.AcquireSync(path)
	if err != nil {
		t.Fatal(err)
	}
	// Pi models-store.test.ts cancels after 10 ms while the file lock is held.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	store := NewFileModelsStore(path)
	err = store.Write(ctx, "two", ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{json.RawMessage(`{"id":"cancelled"}`)})})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cancelled write: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != original {
		t.Fatalf("cancelled write changed catalog: %s, %v", stored, err)
	}
}

func TestFileModelsStoreUsesPiDirectoryLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	store := NewFileModelsStore(path)
	if err := store.Write(t.Context(), "probe", ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{json.RawMessage(`{"id":"probe"}`)})}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("model-store lock remains (Pi cannot acquire a regular flock file): %v", err)
	}
}
