package ai

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// writeAuthBackendFixture protects the Go store's read cache as its production mutation caller does. The callback runs outside this mutex, so it cannot hide premature admission through the filesystem lock.
func writeAuthBackendFixture(storage *AuthStorage, credentials map[string]Credential, lock *pilock.Lock) error {
	storage.mu.Lock()
	defer storage.mu.Unlock()
	return storage.writeLocked(newAuthStorageData(credentials), lock)
}

// packages/coding-agent/test/auth-storage.test.ts:280: backend construction itself has no filesystem effect, and an aborted operation cannot create the file or invoke the update.
func TestAuthStoragePreAbortedFileOperationUpstream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	storage := &AuthStorage{path: path}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	err := storage.withFileLock(ctx, true, func(*pilock.Lock) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("error = %v, update called = %v", err, called)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backing file exists: %v", err)
	}
}

// packages/coding-agent/test/auth-storage.test.ts:293. The 10/150ms boundaries are the original test's waits; the held directory lock deterministically prevents callback admission.
func TestAuthStorageHeldFileLockCancellationUpstream(t *testing.T) {
	storage, path := authPortFile(t, `{"anthropic":{"type":"api_key","key":"stored"}}`)
	lease, err := pilock.Acquire(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Release(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- storage.withFileLock(ctx, true, func(lock *pilock.Lock) error {
			calls.Add(1)
			return writeAuthBackendFixture(storage, map[string]Credential{}, lock)
		})
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting mutation = %v", err)
	}
	if calls.Load() != 0 {
		t.Error("cancelled mutation ran")
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != 0 {
		t.Error("cancelled mutation ran after lock release")
	}
	authPortDisk(t, path, `{"anthropic":{"type":"api_key","key":"stored"}}`)
}

// packages/coding-agent/test/auth-storage.test.ts:333. The backend's actual lock and write primitives retain callback ownership after cancellation and reject its uncommitted replacement.
func TestAuthStorageCancelledFileCallbackRetainsOwnershipUpstream(t *testing.T) {
	storage, path := authPortFile(t, `{"anthropic":{"type":"api_key","key":"stored"}}`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, finish := make(chan struct{}), make(chan struct{})
	pending := make(chan error, 1)
	go func() {
		pending <- storage.withFileLock(ctx, true, func(lock *pilock.Lock) error {
			close(started)
			<-finish
			return writeAuthBackendFixture(storage, map[string]Credential{"openai": {Type: CredentialAPIKey, Key: "cancelled"}}, lock)
		})
	}()
	<-started
	cancel()
	var calls atomic.Int32
	competing := make(chan error, 1)
	go func() {
		competing <- storage.withFileLock(t.Context(), true, func(lock *pilock.Lock) error {
			calls.Add(1)
			return writeAuthBackendFixture(storage, map[string]Credential{"google": {Type: CredentialAPIKey, Key: "committed"}}, lock)
		})
	}()
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 0 {
		t.Error("competing callback ran while cancelled callback remained active")
	}
	close(finish)
	if err := <-pending; !errors.Is(err, context.Canceled) {
		t.Fatalf("active mutation = %v", err)
	}
	if err := <-competing; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("competing callbacks = %d, want 1", calls.Load())
	}
	authPortDisk(t, path, `{"google":{"type":"api_key","key":"committed"}}`)
}
