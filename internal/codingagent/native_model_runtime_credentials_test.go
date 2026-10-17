package codingagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Every native credential read goes through registryCredentials.current. It took the registry's exclusive lock on every Read and List, so the availability pass's provider goroutines queued on it and blocked every RLock user (registryAuthConfig, modelProviderIDs, NativeProvider) while they did.
func TestRegistryCredentialReadsDoNotTakeTheRegistryLock(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	r.SetCredentialStore(ai.NewInMemoryAuthStorage(map[string]ai.Credential{"anthropic": {Type: ai.CredentialAPIKey, Key: "stored"}}))
	credentials := registryCredentials{r}
	ctx := t.Context()
	// The first read after SetCredentialStore creates the overlay under the lock.
	if _, err := credentials.Read(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	done := make(chan error, 1)
	go func() {
		credential, err := credentials.Read(ctx, "anthropic")
		if err == nil && (credential == nil || credential.Key != "stored") {
			t.Errorf("Read = %+v", credential)
		}
		if err == nil {
			_, err = credentials.List(ctx)
		}
		done <- err
	}()
	select {
	case err := <-done:
		r.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		r.mu.Unlock()
		<-done
		t.Fatal("a credential read waited for the registry lock")
	}
}

// SetCredentialStore still discards the runtime-key overlay, and runtime-key setters still order against it under the registry lock, while readers load the overlay without the lock. Run with -race.
func TestRegistryCredentialOverlayRacesStoreSwapsAndRuntimeKeys(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	stores := []ai.CredentialStore{
		ai.NewInMemoryAuthStorage(map[string]ai.Credential{"anthropic": {Type: ai.CredentialAPIKey, Key: "a"}}),
		ai.NewInMemoryAuthStorage(map[string]ai.Credential{"anthropic": {Type: ai.CredentialAPIKey, Key: "b"}}),
	}
	r.SetCredentialStore(stores[0])
	credentials := registryCredentials{r}
	ctx := context.Background()
	const rounds = 2000
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range rounds {
			r.SetCredentialStore(stores[i%2])
		}
	})
	wg.Go(func() {
		for range rounds {
			r.SetRuntimeAPIKey("anthropic", "runtime")
			r.RemoveRuntimeAPIKey("anthropic")
		}
	})
	for range 4 {
		wg.Go(func() {
			for range rounds {
				credential, err := credentials.Read(ctx, "anthropic")
				if err != nil {
					t.Error(err)
					return
				}
				if credential == nil || (credential.Key != "a" && credential.Key != "b" && credential.Key != "runtime") {
					t.Errorf("Read = %+v", credential)
					return
				}
				if _, err := credentials.List(ctx); err != nil {
					t.Error(err)
					return
				}
				if key, ok := r.RuntimeAPIKey("anthropic"); ok && key != "runtime" {
					t.Errorf("RuntimeAPIKey = %q", key)
					return
				}
				r.GetProviderAuthStatus("anthropic")
			}
		})
	}
	wg.Wait()
	// The last swap discards every runtime key set before it.
	r.SetCredentialStore(stores[1])
	if _, ok := r.RuntimeAPIKey("anthropic"); ok {
		t.Fatal("SetCredentialStore kept a runtime key")
	}
	credential, err := credentials.Read(ctx, "anthropic")
	if err != nil || credential == nil || credential.Key != "b" {
		t.Fatalf("Read after swap = %+v, %v", credential, err)
	}
}
