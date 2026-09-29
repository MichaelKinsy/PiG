package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/ai/src/models.ts:414-417,489-498 passes the refresh signal to CredentialStore.read, including the offline restoration phase.
type blockingRuntimeCredentialStore struct {
	ai.CredentialStore
	entered            chan context.Context
	release            chan struct{}
	once               sync.Once
	ignoreCancellation bool
	providerID         string
}

func (store *blockingRuntimeCredentialStore) Read(ctx context.Context, providerID string) (*ai.Credential, error) {
	target := store.providerID
	if target == "" {
		target = "radius"
	}
	if providerID != target {
		return store.CredentialStore.Read(ctx, providerID)
	}
	store.once.Do(func() { store.entered <- ctx })
	if store.ignoreCancellation {
		<-store.release
		return nil, nil
	}
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-store.release:
		return nil, nil
	}
}

// Pi packages/ai/src/models.ts:432-457 races an uncooperative store read against the signal. The runtime still owns the read until it settles.
func TestInjectedCredentialStoreRefreshCancellationDoesNotWaitForRead(t *testing.T) {
	for _, providerID := range []string{"radius", "native-custom"} {
		t.Run(providerID, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := &blockingRuntimeCredentialStore{
					CredentialStore: ai.NewInMemoryAuthStorage(nil),
					entered:         make(chan context.Context, 1), release: make(chan struct{}), ignoreCancellation: true, providerID: providerID,
				}
				runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{
					Credentials: store, ModelsPath: new((*string)(nil)), RefreshOnCreate: new(false),
				})
				if err != nil {
					t.Fatal(err)
				}
				defer runtime.Close()
				defer func() { close(store.release); synctest.Wait() }()
				if providerID == "native-custom" {
					if err := runtime.RegisterNativeProvider(&ai.ModelsProvider{
						ID: providerID, GetModels: func() ([]*ai.Model, error) { return nil, nil },
						RefreshModels: func(ai.RefreshModelsContext) error { return nil },
						Auth:          ai.ProviderAuth{APIKey: &ai.APIKeyAuth{}},
					}); err != nil {
						t.Fatal(err)
					}
					// The registration's local refresh is queued (D84), so no Services-owned read starts at registration. A provider availability pass starts the Services-owned read that Close must wait for; the caller's Refresh below is caller-owned and Services.Close does not wait for it.
					go runtime.refreshAvailability(t.Context(), ai.ModelsRefreshOptions{Providers: []string{providerID}}, ai.ModelsRefreshResult{})
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var result ai.ModelsRefreshResult
				settled := false
				go func() {
					result = runtime.Refresh(ctx, ai.ModelsRefreshOptions{Providers: []string{providerID}, AllowNetwork: new(false)})
					settled = true
				}()
				<-store.entered
				cancel()
				synctest.Wait()
				if !settled {
					t.Fatal("cancelled refresh still waits for an uncooperative credential read")
				}
				if !result.Aborted || len(result.Errors) != 0 {
					t.Fatalf("refresh result = %+v, want aborted without provider errors", result)
				}
				closed := false
				go func() { runtime.Close(); closed = true }()
				synctest.Wait()
				if closed {
					t.Fatal("runtime closed before its credential read settled")
				}
			})
		})
	}
}

// Pi model-runtime.ts:174 does not construct the default store when credentials are supplied, even when authPath cannot be opened.
func TestInjectedCredentialStoreDoesNotOpenAuthPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("not auth storage"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{
		Credentials: ai.NewInMemoryAuthStorage(nil), AuthPath: filepath.Join(path, "auth.json"), ModelsPath: new((*string)(nil)),
	})
	if err != nil {
		t.Fatalf("injected credentials opened authPath: %v", err)
	}
	runtime.Close()
}

func BenchmarkInjectedCredentialAvailability(b *testing.B) {
	store := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"anthropic": {Type: ai.CredentialAPIKey, Key: "benchmark-key"}})
	runtime, err := CreateModelRuntime(b.Context(), CreateModelRuntimeOptions{Credentials: store, ModelsPath: new((*string)(nil))})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(runtime.Close)
	b.ReportAllocs()
	for b.Loop() {
		models, err := runtime.GetAvailable(b.Context(), "anthropic")
		if err != nil || len(models) == 0 {
			b.Fatalf("scoped availability: models=%d, err=%v", len(models), err)
		}
	}
}

func TestInjectedCredentialStoreRefreshReadReceivesCancellation(t *testing.T) {
	store := &blockingRuntimeCredentialStore{
		CredentialStore: ai.NewInMemoryAuthStorage(nil),
		entered:         make(chan context.Context, 1),
		release:         make(chan struct{}),
	}
	runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{
		Credentials: store, ModelsPath: new((*string)(nil)), RefreshOnCreate: new(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	done := make(chan ai.ModelsRefreshResult, 1)
	go func() {
		done <- runtime.Refresh(ctx, ai.ModelsRefreshOptions{Providers: []string{"radius"}, AllowNetwork: new(false)})
	}()
	defer func() {
		close(store.release)
		result := <-done
		if !result.Aborted || len(result.Errors) != 0 {
			t.Errorf("refresh result = %+v, want aborted without provider errors", result)
		}
	}()
	readContext := <-store.entered
	reason := errors.New("cancel credential restoration")
	cancel(reason)
	if !errors.Is(context.Cause(readContext), reason) {
		t.Fatalf("credential read lost refresh cancellation: %v", context.Cause(readContext))
	}
}
