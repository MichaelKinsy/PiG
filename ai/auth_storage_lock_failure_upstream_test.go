package ai

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

func stubAuthFileLockAcquire(t *testing.T, stub func(ctx context.Context, path string) (*pilock.Lock, error)) {
	t.Helper()
	previous := acquireAuthFileLock
	acquireAuthFileLock = stub
	t.Cleanup(func() { acquireAuthFileLock = previous })
}

// packages/coding-agent/test/auth-storage.test.ts:225 "does not write after lock acquisition failure and recovers on retry".
func TestAuthStorageLockAcquisitionFailureDoesNotWriteAndRecoversUpstream(t *testing.T) {
	storage, path := authPortFile(t, `{"anthropic":{"type":"api_key","key":"stored"}}`)
	real := acquireAuthFileLock
	var fail atomic.Bool
	fail.Store(true)
	stubAuthFileLockAcquire(t, func(ctx context.Context, path string) (*pilock.Lock, error) {
		if fail.CompareAndSwap(true, false) {
			return nil, errors.New("lock unavailable")
		}
		return real(ctx, path)
	})
	_, err := storage.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "lock unavailable") {
		t.Fatalf("modify error = %v, want lock unavailable", err)
	}
	authPortDisk(t, path, `{"anthropic":{"type":"api_key","key":"stored"}}`)

	if _, err := storage.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	authPortDisk(t, path, `{"anthropic":{"type":"api_key","key":"stored"},"openai":{"type":"api_key","key":"new"}}`)
}

// packages/coding-agent/test/auth-storage.test.ts:245 "retries a briefly contended file lock": a lock held briefly delays, but does not fail, the operation; the update runs once and the lock is released once. Go's ELOCKED retry loop lives inside pilock.Acquire, so the retry count is not observable at the acquire seam; the test releases the held lock only after the operation has entered acquisition.
func TestAuthStorageRetriesBrieflyContendedFileLockUpstream(t *testing.T) {
	storage, path := authPortFile(t, `{"anthropic":{"type":"api_key","key":"stored"}}`)
	held, err := pilock.Acquire(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	var attempts, releases atomic.Int32
	entered := make(chan struct{})
	real := acquireAuthFileLock
	stubAuthFileLockAcquire(t, func(ctx context.Context, path string) (*pilock.Lock, error) {
		attempts.Add(1)
		close(entered)
		return real(ctx, path)
	})
	previousRelease := releaseAuthFileLock
	releaseAuthFileLock = func(lock *pilock.Lock) error {
		releases.Add(1)
		return previousRelease(lock)
	}
	t.Cleanup(func() { releaseAuthFileLock = previousRelease })

	var updates atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := storage.Modify(t.Context(), "anthropic", func(*Credential) (*Credential, error) {
			updates.Add(1)
			return nil, nil
		})
		done <- err
	}()
	<-entered
	if updates.Load() != 0 {
		t.Fatal("update ran while the lock was held")
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 1 || updates.Load() != 1 || releases.Load() != 1 {
		t.Fatalf("acquire attempts=%d updates=%d releases=%d, want 1 each", attempts.Load(), updates.Load(), releases.Load())
	}
}

// packages/coding-agent/test/auth-storage.test.ts:263 "surfaces a compromised file storage lock": compromise reported at acquisition; the update never runs and the file is unchanged.
func TestAuthStorageCompromisedLockAtAcquisitionUpstream(t *testing.T) {
	storage, path := authPortFile(t, `{"anthropic":{"type":"api_key","key":"stored"}}`)
	real := acquireAuthFileLock
	stubAuthFileLockAcquire(t, func(ctx context.Context, p string) (*pilock.Lock, error) {
		lock, err := real(ctx, p)
		if err == nil {
			if rmErr := os.Remove(p + ".lock"); rmErr != nil {
				t.Fatal(rmErr)
			}
		}
		return lock, err
	})
	var calls atomic.Int32
	err := storage.withFileLock(t.Context(), true, func(lock *pilock.Lock) error {
		calls.Add(1)
		return writeAuthBackendFixture(storage, map[string]Credential{}, lock)
	})
	if err == nil || calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d, want an error and no update", err, calls.Load())
	}
	authPortDisk(t, path, `{"anthropic":{"type":"api_key","key":"stored"}}`)
}

// packages/coding-agent/test/auth-storage.test.ts:314 "releases a file lock acquired concurrently with cancellation before mutation": the lock is held when the signal aborts; the operation rejects with the abort, the update never runs and the lock is released exactly once.
func TestAuthStorageReleasesLockAcquiredConcurrentlyWithCancellationUpstream(t *testing.T) {
	storage, path := authPortFile(t, `{"anthropic":{"type":"api_key","key":"stored"}}`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	real := acquireAuthFileLock
	var acquired atomic.Bool
	stubAuthFileLockAcquire(t, func(ctx context.Context, path string) (*pilock.Lock, error) {
		lock, err := real(ctx, path)
		acquired.Store(err == nil && lock != nil)
		cancel()
		return lock, err
	})
	var releases atomic.Int32
	previousRelease := releaseAuthFileLock
	releaseAuthFileLock = func(lock *pilock.Lock) error {
		releases.Add(1)
		return previousRelease(lock)
	}
	t.Cleanup(func() { releaseAuthFileLock = previousRelease })
	var updates atomic.Int32
	_, err := storage.Modify(ctx, "anthropic", func(*Credential) (*Credential, error) {
		updates.Add(1)
		return &Credential{Type: CredentialAPIKey, Key: "must-not-write"}, nil
	})
	if !acquired.Load() {
		t.Fatal("stub did not acquire the lock")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("modify error = %v, want cancellation", err)
	}
	if updates.Load() != 0 {
		t.Fatal("mutation ran after cancellation")
	}
	if releases.Load() != 1 {
		t.Fatalf("releases = %d, want 1", releases.Load())
	}
	if _, statErr := os.Stat(path + ".lock"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("lock left behind: %v", statErr)
	}
	authPortDisk(t, path, `{"anthropic":{"type":"api_key","key":"stored"}}`)
}

// packages/coding-agent/test/auth-storage.test.ts:544 "does not overwrite malformed auth files".
func TestAuthStorageModifyDoesNotOverwriteMalformedFileUpstream(t *testing.T) {
	storage, path := authPortFile(t, `{"anthropic":{"type":"api_key","key":"stored"}}`)
	if err := os.WriteFile(path, []byte("{invalid-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Modify(t.Context(), "openai", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "new"}, nil
	}); err == nil {
		t.Fatal("modify over a malformed file succeeded")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{invalid-json" {
		t.Fatalf("malformed file changed: %q, %v", data, err)
	}
}

// packages/coding-agent/test/auth-storage.test.ts:489 "translates a credential-store refresh failure and allows a later retry".
func TestModelsGetAuthTranslatesCredentialStoreRefreshFailureUpstream(t *testing.T) {
	const providerID = "oauth-provider"
	base := NewInMemoryAuthStorage(map[string]Credential{providerID: {Type: CredentialOAuth, Access: "expired-access", Refresh: "refresh-token", Expires: 0}})
	store := &failingModifyStore{InMemoryAuthStorage: base}
	store.failNext.Store(true)
	provider := modelsRuntimeProvider(modelsRuntimeProviderInput{id: providerID, auth: &ProviderAuth{OAuth: &OAuthAuth{
		Name: "OAuth",
		Login: func(context.Context, AuthInteraction) (Credential, error) {
			return Credential{}, errors.New("not used")
		},
		Refresh: func(_ context.Context, credential Credential) (Credential, error) {
			credential.Access = "refreshed-access"
			credential.Expires = time.Now().UnixMilli() + 60_000
			return credential, nil
		},
		ToAuth: func(credential Credential) (ModelAuth, error) { return ModelAuth{APIKey: credential.Access}, nil },
	}}})
	models := CreateModels(CreateModelsOptions{Credentials: store})
	t.Cleanup(models.Close)
	models.SetProvider(provider)

	_, err := models.GetAuth(t.Context(), providerID)
	var modelsErr *ModelsError
	if !errors.As(err, &modelsErr) || modelsErr.Code != ModelsErrorAuth {
		t.Fatalf("first GetAuth error = %v, want code auth", err)
	}
	result, err := models.GetAuth(t.Context(), providerID)
	if err != nil || result == nil || result.Auth.APIKey != "refreshed-access" {
		t.Fatalf("retry GetAuth = %+v, %v", result, err)
	}
}

type failingModifyStore struct {
	*InMemoryAuthStorage
	failNext atomic.Bool
}

func (s *failingModifyStore) Modify(ctx context.Context, id string, fn func(*Credential) (*Credential, error)) (*Credential, error) {
	if s.failNext.CompareAndSwap(true, false) {
		return nil, errors.New("credential store unavailable")
	}
	return s.InMemoryAuthStorage.Modify(ctx, id, fn)
}
