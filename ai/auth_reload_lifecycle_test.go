package ai

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

func isolateAuthReloadState(t *testing.T) {
	t.Helper()
	sharedAuthFileReadState.Lock()
	path, state := sharedAuthFileReadState.path, sharedAuthFileReadState.state
	sharedAuthFileReadState.path, sharedAuthFileReadState.state = "", nil
	sharedAuthFileReadState.Unlock()
	t.Cleanup(func() {
		sharedAuthFileReadState.Lock()
		sharedAuthFileReadState.path, sharedAuthFileReadState.state = path, state
		sharedAuthFileReadState.Unlock()
	})
}

// Pi auth-storage.test.ts:116: canceling one reader must not cancel the coalesced reload while a second reader still waits.
func TestAuthStorageCoalescedReloadSurvivesReaderCancellationUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		storage, path := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"old"}}`)
		authReloadRead(t, storage, "anthropic", &Credential{Type: CredentialAPIKey, Key: "old"})
		authReloadWrite(t, path, `{"anthropic":{"type":"api_key","key":"new"}}`)
		grant := make(chan struct{})
		entered := make(chan struct{})
		var locks, releases atomic.Int32
		release := releaseAuthFileLock
		releaseAuthFileLock = func(lock *pilock.Lock) error {
			releases.Add(1)
			return release(lock)
		}
		t.Cleanup(func() { releaseAuthFileLock = release })
		acquire := acquireAuthFileLock
		acquireAuthFileLock = func(ctx context.Context, path string) (*pilock.Lock, error) {
			if locks.Add(1) == 1 {
				close(entered)
			}
			<-grant
			return acquire(ctx, path)
		}
		t.Cleanup(func() { acquireAuthFileLock = acquire })
		firstCtx, cancelFirst := context.WithCancel(t.Context())
		defer cancelFirst()
		first := make(chan error, 1)
		second := make(chan error, 1)
		go func() { _, err := storage.Read(firstCtx, "anthropic"); first <- err }()
		// Pi invokes the first async read before the second. Its body reaches lock acquisition synchronously, so the canceled reader must own the shared reload in this guard.
		<-entered
		go func() {
			got, err := storage.Read(t.Context(), "anthropic")
			if err == nil && (got == nil || got.Key != "new") {
				err = errors.New("wrong credential")
			}
			second <- err
		}()
		synctest.Wait()
		cancelFirst()
		synctest.Wait()
		select {
		case err := <-first:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("first=%v", err)
			}
		default:
			t.Error("canceled reader still waits for reload")
		}
		close(grant)
		if err := <-second; err != nil {
			t.Fatal(err)
		}
		if locks.Load() != 1 || releases.Load() != 1 {
			t.Fatalf("locks=%d releases=%d want one shared acquisition and release", locks.Load(), releases.Load())
		}
		if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reload did not release lock: %v", err)
		}
	})
}

// Pi auth-storage.test.ts:373: the last canceled reader stops lock acquisition, and the next read starts a fresh reload rather than inheriting its rejection.
func TestAuthStorageReadCancellationStopsReloadUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		storage, path := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"old"}}`)
		authReloadRead(t, storage, "anthropic", &Credential{Type: CredentialAPIKey, Key: "old"})
		authReloadWrite(t, path, `{"anthropic":{"type":"api_key","key":"new-value"}}`)
		acquire := acquireAuthFileLock
		var locks atomic.Int32
		stopped := make(chan struct{})
		acquireAuthFileLock = func(ctx context.Context, path string) (*pilock.Lock, error) {
			if locks.Add(1) == 1 {
				<-ctx.Done()
				close(stopped)
				return nil, context.Cause(ctx)
			}
			return acquire(ctx, path)
		}
		t.Cleanup(func() { acquireAuthFileLock = acquire })
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		cause := errors.New("reader cancelled")
		done := make(chan error, 1)
		go func() { _, err := storage.Read(ctx, "anthropic"); done <- err }()
		synctest.Wait()
		cancel(cause)
		if err := <-done; !errors.Is(err, cause) {
			t.Errorf("reader error=%v want original cause", err)
		}
		<-stopped
		synctest.Wait()
		storage.read.mu.Lock()
		active := storage.read.reload != nil
		storage.read.mu.Unlock()
		if active {
			t.Fatal("canceled reload remains active")
		}
		authReloadRead(t, storage, "anthropic", &Credential{Type: CredentialAPIKey, Key: "new-value"})
		if locks.Load() != 2 {
			t.Fatalf("acquisitions=%d want canceled attempt plus fresh read", locks.Load())
		}
	})
}

func TestAuthStorageFailedReloadRetainsSnapshotAndRetries(t *testing.T) {
	storage, path := authReloadFile(t, `{"anthropic":{"type":"api_key","key":"old"}}`)
	authReloadRead(t, storage, "anthropic", &Credential{Type: CredentialAPIKey, Key: "old"})
	authReloadWrite(t, path, `{invalid-json`)
	if _, err := storage.Read(t.Context(), "anthropic"); err == nil {
		t.Fatal("signalled read suppressed parse failure")
	}
	// Pi readLatestData catches reload failure only for unsignalled readers.
	got, err := storage.Read(context.Background(), "anthropic")
	if err != nil || got == nil || got.Key != "old" {
		t.Errorf("unsignalled read lost last valid snapshot: %+v, %v", got, err)
	}
	authReloadWrite(t, path, `{"anthropic":{"type":"api_key","key":"repaired"}}`)
	authReloadRead(t, storage, "anthropic", &Credential{Type: CredentialAPIKey, Key: "repaired"})
}

func TestAuthStorageUnsignalledFallbackTracksMutations(t *testing.T) {
	for _, operation := range []string{"set", "update", "modify", "unchanged modify", "delete"} {
		t.Run(operation, func(t *testing.T) {
			storage, path := authReloadFile(t, `{"custom":{"type":"api_key","key":"old"}}`)
			fresh := Credential{Type: CredentialAPIKey, Key: "committed"}
			var err error
			switch operation {
			case "set":
				err = storage.Set("custom", fresh)
			case "update":
				err = storage.Update("custom", func(Credential, bool) (Credential, error) { return fresh, nil })
			case "modify":
				_, err = storage.Modify(t.Context(), "custom", func(*Credential) (*Credential, error) { return &fresh, nil })
			case "unchanged modify":
				authReloadWrite(t, path, `{"custom":{"type":"api_key","key":"committed"}}`)
				_, err = storage.Modify(t.Context(), "custom", func(*Credential) (*Credential, error) { return nil, nil })
			case "delete":
				err = storage.Delete(t.Context(), "custom")
			}
			if err != nil {
				t.Fatal(err)
			}
			authReloadWrite(t, path, `{invalid-json`)
			got, err := storage.Read(context.Background(), "custom")
			if err != nil {
				t.Fatal(err)
			}
			if operation == "delete" {
				if got != nil {
					t.Fatalf("fallback restored deleted credential: %+v", got)
				}
			} else if got == nil || got.Key != fresh.Key {
				t.Fatalf("fallback lost committed snapshot: %+v", got)
			}
		})
	}
}

func TestAuthStorageSharedCacheRetainsOnlyFirstPath(t *testing.T) {
	isolateAuthReloadState(t)
	first, path := authReloadFile(t, `{}`)
	second, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	other, otherPath := authReloadFile(t, `{}`)
	otherSecond, err := NewAuthStorage(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	third, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.read != second.read || first.read != third.read {
		t.Fatal("first path lost shared read state")
	}
	if other.read == first.read || otherSecond.read == other.read {
		t.Fatal("non-primary paths entered process cache")
	}
}
