package ai

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// FileModelsStore writes through FileAuthStorageBackend.withLockAsync, whose checkpoints are: after fn, throwIfCompromised and then the abort; after the write, throwIfCompromised alone (auth-storage.ts:183-189, unchanged in 0.99.1). So a lock compromised while fn ran wins over an abort that also arrived, and a compromise noticed after the write fails the write even though the file changed. TestAuthStorageLockCompromisedDuringModifyUpstream runs Pi's own withLockAsync for the auth store through the same checkpoints.
func TestFileModelsStoreLockCheckpointsFollowPi(t *testing.T) {
	compromised := errors.New("lock compromised")
	entry := ModelsStoreEntry{Models: []json.RawMessage{}}

	t.Run("compromise wins over an abort after fn", func(t *testing.T) {
		store := NewFileModelsStore(filepath.Join(t.TempDir(), "models-store.json"))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		// The abort arrives after the acquisition's checks, while the store reads and fn runs.
		store.lock = func(_ context.Context, _ string, fn func(func() error) error) error {
			cancel()
			return fn(func() error { return compromised })
		}
		if err := store.Write(ctx, "probe", entry); !errors.Is(err, compromised) {
			t.Fatalf("Write error = %v, want the compromise", err)
		}
	})

	t.Run("compromise after the write fails the write", func(t *testing.T) {
		store := NewFileModelsStore(filepath.Join(t.TempDir(), "models-store.json"))
		checks := 0
		store.lock = func(_ context.Context, _ string, fn func(func() error) error) error {
			return fn(func() error {
				checks++
				if checks > 1 {
					return compromised
				}
				return nil
			})
		}
		if err := store.Write(t.Context(), "probe", entry); !errors.Is(err, compromised) {
			t.Fatalf("Write error = %v after %d checks, want the compromise noticed after the write", err, checks)
		}
	})
}

// withSidecarLock hands fn withLockAsync's throwIfCompromised alone: an abort that arrives after the lock is held is not a compromise, so the check fn runs after the write reports nothing for it (auth-storage.ts:189).
func TestWithSidecarLockHandsACompromiseOnlyCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	path := filepath.Join(t.TempDir(), "models-store.json")
	err := withSidecarLock(ctx, path, func(check func() error) error {
		cancel()
		return check()
	})
	if err != nil {
		t.Fatalf("the handed check after an abort = %v, want no compromise", err)
	}
}
