package ai

// Pi: packages/coding-agent/src/core/models-store.ts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// models-store.ts:129-138 write runs its callback under storage.withLockAsync(fn, options), which throws the signal's reason when it aborted during the locked callback, after fn and before writeFileSync (auth-storage.ts:186-190): a cancelled write must not commit.
func TestFileModelsStoreWriteAbortedInsideTheLockDoesNotCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-store.json")
	const before = "{}"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewFileModelsStore(path)
	ctx, cancel := context.WithCancel(t.Context())
	store.lock = func(_ context.Context, _ string, fn func(func() error) error) error {
		// The abort arrives once the lock is held, after the operation's start checks.
		cancel()
		return fn(func() error { return nil })
	}
	err := store.Write(ctx, "one", ModelsStoreEntry{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Write = %v, want context.Canceled", err)
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != before {
		t.Fatalf("file after the aborted write = %q, %v, want %q untouched", data, readErr, before)
	}
}
