package ai

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Pi FileAuthStorageBackend uses proper-lockfile lock directories and removes
// them on release. A regular .lock file blocks an independent Node ModelRuntime.
func TestGoStoresReleasePiDirectoryLocks(t *testing.T) {
	root := t.TempDir()
	auth, err := NewAuthStorage(filepath.Join(root, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("private", Credential{Type: CredentialAPIKey, Key: "key"}); err != nil {
		t.Fatal(err)
	}
	models := NewFileModelsStore(filepath.Join(root, "models-store.json"))
	if err := models.Write(t.Context(), "private", ModelsStoreEntry{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"auth.json", "models-store.json"} {
		if _, err := os.Stat(filepath.Join(root, name+".lock")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s leaves a sidecar instead of releasing Pi's directory lock: %v", name, err)
		}
	}
}

func TestSharedAuthDirectoryLockWaitIsCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	auth, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	timer := time.AfterFunc(30*time.Millisecond, cancel)
	defer timer.Stop()
	_, err = auth.Modify(ctx, "private", func(*Credential) (*Credential, error) {
		t.Error("mutator ran while another process held the lock")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("contended async lock = %v, want cancellation", err)
	}
}
