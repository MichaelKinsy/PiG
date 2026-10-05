package codingagent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

func BenchmarkNativeCredentialCycle(b *testing.B) {
	r := NewModelRegistry(b.TempDir())
	r.runtimeCredentials = ai.NewRuntimeCredentials(ai.NewInMemoryAuthStorage(nil))
	provider := credentialTestProvider("benchmark")
	if err := r.RegisterNativeModelsProvider(provider); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(r.NativeModels().Close)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.LoginNativeProvider(b.Context(), provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{}, nil); err != nil {
			b.Fatal(err)
		}
		if err := r.LogoutNativeProvider(b.Context(), provider.ID); err != nil {
			b.Fatal(err)
		}
	}
	if len(r.credentialOperations) != 0 {
		b.Fatal("credential queue retained completed operations")
	}
}

// Pi model-runtime.ts:509-531 cancels a queued operation without executing it or releasing its predecessor's ownership.
func TestCredentialRuntimeQueuedCancellationRetainsOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, finish := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		provider := credentialTestProvider("queued")
		provider.Auth.APIKey.Login = func(context.Context, ai.AuthInteraction) (ai.Credential, error) {
			calls.Add(1)
			close(started)
			<-finish
			return ai.Credential{Type: ai.CredentialAPIKey, Key: "queued-key"}, nil
		}
		store := ai.NewInMemoryAuthStorage(nil)
		r := credentialTestRuntime(t, store, provider)
		login := make(chan error, 1)
		go func() {
			_, err := r.LoginNativeProvider(t.Context(), provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{}, nil)
			login <- err
		}()
		<-started
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		queued := make(chan error, 1)
		go func() {
			_, err := r.LoginNativeProvider(ctx, provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{}, nil)
			queued <- err
		}()
		synctest.Wait()
		logout := make(chan error, 1)
		go func() { logout <- r.LogoutNativeProvider(t.Context(), provider.ID) }()
		synctest.Wait()
		cause := errors.New("queued caller cancelled")
		cancel(cause)
		if err := <-queued; !errors.Is(err, cause) {
			t.Fatalf("queued result = %v", err)
		}
		synctest.Wait()
		select {
		case err := <-logout:
			t.Fatalf("logout overtook active login: %v", err)
		default:
		}
		close(finish)
		if err := <-login; err != nil {
			t.Fatal(err)
		}
		if err := <-logout; err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 {
			t.Fatal("cancelled login callback ran")
		}
		requireStoredRuntimeKey(t, store, provider.ID, "")
		if len(r.credentialOperations) != 0 {
			t.Fatal("completed credential queue retained waiters")
		}
	})
}

// Pi keeps the credential-operation tail through failed synchronization and allows its successor to run afterward.
func TestCredentialRuntimeOrdersThroughSynchronizationFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, finish := make(chan struct{}), make(chan struct{})
		var fail atomic.Bool
		provider := credentialTestProvider("sync-queue")
		cause := errors.New("local snapshot failed")
		provider.RefreshModels = func(ai.RefreshModelsContext) error {
			if fail.Swap(false) {
				close(started)
				<-finish
				return cause
			}
			return nil
		}
		store := ai.NewInMemoryAuthStorage(nil)
		r := credentialTestRuntime(t, store, provider)
		fail.Store(true)
		login, logout := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := r.LoginNativeProvider(t.Context(), provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{}, nil)
			login <- err
		}()
		<-started
		go func() { logout <- r.LogoutNativeProvider(t.Context(), provider.ID) }()
		synctest.Wait()
		requireStoredRuntimeKey(t, store, provider.ID, "sync-queue-key")
		close(finish)
		err := <-login
		requireCredentialSynchronizationError(t, err, provider.ID)
		if !errors.Is(err, cause) {
			t.Fatalf("synchronization cause lost: %v", err)
		}
		if err := <-logout; err != nil {
			t.Fatal(err)
		}
		requireStoredRuntimeKey(t, store, provider.ID, "")
		if len(r.credentialOperations) != 0 {
			t.Fatal("failed credential queue retained waiters")
		}
	})
}

// Pi model-runtime.ts:344-392,534-557 surfaces post-commit availability failures just like catalog failures.
func TestCredentialRuntimePostCommitAvailabilityErrors(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "error"
		if cancelled {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			var fail atomic.Bool
			started := make(chan struct{})
			cause := errors.New("availability failed")
			provider := credentialTestProvider("availability")
			check := provider.Auth.APIKey.Check
			provider.Auth.APIKey.Check = func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
				if fail.Load() {
					close(started)
					if cancelled {
						<-ctx.Done()
						return nil, context.Cause(ctx)
					}
					return nil, cause
				}
				return check(ctx, input)
			}
			store := ai.NewInMemoryAuthStorage(nil)
			r := credentialTestRuntime(t, store, provider)
			fail.Store(true)
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			done := make(chan error, 1)
			go func() {
				_, err := r.LoginNativeProvider(ctx, provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{}, nil)
				done <- err
			}()
			<-started
			if cancelled {
				cancel(cause)
			}
			err := <-done
			requireCredentialSynchronizationError(t, err, provider.ID)
			if !errors.Is(err, cause) {
				t.Fatalf("availability cause lost: %v", err)
			}
			requireStoredRuntimeKey(t, store, provider.ID, "availability-key")
		})
	}
}
