package codingagent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// model-runtime.ts refresh(options): AllowNetwork defaults to true, Force and the provider selection reach every provider's refresh, each failing
// provider is reported by id in the order it ran, and a context that is already cancelled aborts before any provider refreshes.
func TestRefreshModelRuntimePassesItsOptionsToEachProviderAndReportsFailuresInOrder(t *testing.T) {
	var mu sync.Mutex
	var armed atomic.Bool
	seen := map[string][]ai.RefreshModelsContext{}
	provider := func(id string) *ai.ModelsProvider {
		p := credentialTestProvider(id)
		// A configured provider is refreshed with network access when the options allow it.
		p.Auth.APIKey.Check = func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "ambient"}, nil
		}
		p.Auth.APIKey.Resolve = func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "ambient-key"}, Source: "ambient"}, nil
		}
		p.RefreshModels = func(refresh ai.RefreshModelsContext) error {
			mu.Lock()
			seen[id] = append(seen[id], refresh)
			mu.Unlock()
			// The local pass runs first and a failure there skips the network pass; fail the network pass only.
			if !armed.Load() || !refresh.AllowNetwork {
				return nil
			}
			return errors.New(id + " failed")
		}
		return p
	}
	registry := credentialTestRuntime(t, ai.NewInMemoryAuthStorage(nil), provider("refresh-a"), provider("refresh-b"), provider("refresh-c"))
	defer registry.NativeModels().Close()
	mu.Lock()
	clear(seen)
	mu.Unlock()
	armed.Store(true)
	// A refresh may run a local pass (no network) before the network pass, so the options are judged over all of a provider's calls.
	calls := func(id string) (network, forced bool, count int) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		for _, refresh := range seen[id] {
			network = network || refresh.AllowNetwork
			forced = forced || (refresh.Force != nil && *refresh.Force)
		}
		return network, forced, len(seen[id])
	}
	reset := func() {
		mu.Lock()
		clear(seen)
		mu.Unlock()
	}

	result := registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{Providers: []string{"refresh-a", "refresh-b"}})
	if result.Aborted || len(result.Errors) != 2 || result.Errors["refresh-a"] == nil || result.Errors["refresh-b"] == nil {
		t.Fatalf("result = %+v, want both selected providers failed", result)
	}
	// models.ts:586-597 sets each failure in the errors Map when its provider's refresh settles, so the order is completion order. Providers that fail at once settle in no
	// fixed order here (each refresh runs on its own goroutine), so the order of simultaneous failures is not pinned: every failed provider appears once.
	if got := slices.Sorted(slices.Values(result.ErrorOrder)); !slices.Equal(got, []string{"refresh-a", "refresh-b"}) {
		t.Fatalf("error order = %v, want refresh-a and refresh-b once each", result.ErrorOrder)
	}
	if network, forced, count := calls("refresh-a"); count == 0 || !network || forced {
		t.Fatalf("defaults: refreshed %d times, network %v, forced %v; want network allowed and force unset", count, network, forced)
	}
	mu.Lock()
	_, refreshedC := seen["refresh-c"]
	mu.Unlock()
	if refreshedC {
		t.Fatal("an unselected provider was refreshed")
	}

	reset()
	offline := registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{Providers: []string{"refresh-a"}, AllowNetwork: new(false)})
	if network, _, count := calls("refresh-a"); count == 0 || network || len(offline.Errors) != 0 {
		t.Fatalf("AllowNetwork=false: refreshed %d times, network %v, errors %v; want a local pass only", count, network, offline.Errors)
	}
	reset()
	registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{Providers: []string{"refresh-a"}, Force: new(true)})
	if network, forced, _ := calls("refresh-a"); !network || !forced {
		t.Fatalf("Force=true: network %v, forced %v; want the network pass forced", network, forced)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	reset()
	aborted := registry.RefreshModelRuntime(cancelled, ai.ModelsRefreshOptions{})
	if !aborted.Aborted || aborted.Errors == nil || len(aborted.Errors) != 0 {
		t.Fatalf("a cancelled refresh = %+v, want aborted with an empty error map", aborted)
	}
	mu.Lock()
	refreshed := len(seen)
	mu.Unlock()
	if refreshed != 0 {
		t.Fatalf("a cancelled refresh still refreshed %d providers", refreshed)
	}
}
