package chord

import (
	"context"
	"testing"
	"time"
)

// Upstream closes the generation before emitting its lifecycle update, so a close invoked by that update's listener is a no-op.
func TestKeyedCloseIsReentrant(t *testing.T) {
	ctx := context.Background()
	provider, err := NewRemoteServiceProvider(KeyedService(keyedCounterDefinition))
	if err != nil {
		t.Fatal(err)
	}
	closeInstance, err := Spawn[Counter](provider, keyedCounterDefinition, "instance", newCounter(t))
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	subscription, err := provider.Subscribe(keyedCounterDefinition.Id(), ServiceKeyed, func(_ context.Context, update ServiceProviderUpdate) {
		if update.Type == UpdateClosed {
			closed++
			if err := closeInstance(); err != nil {
				t.Error(err)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := subscription.Activate(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- closeInstance() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reentrant generation close deadlocked")
	}
	if closed != 1 {
		t.Fatalf("closed deliveries = %d, want one for the single generation", closed)
	}
	if err := subscription.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := provider.Dispose(); err != nil {
		t.Fatal(err)
	}
}

// Upstream combineFacetLoaders sets disposed before invoking any loaded generation's disposal callback.
func TestCombinedLoaderDisposeIsReentrant(t *testing.T) {
	ctx := context.Background()
	var loaded LoadedFacets
	calls := 0
	loader := facetLoaderFunc(func(context.Context) (LoadedFacets, error) {
		return LoadedFacets{Dispose: func(ctx context.Context) error {
			calls++
			return loaded.Dispose(ctx)
		}}, nil
	})
	var err error
	loaded, err = CombineFacetLoaders(loader).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- loaded.Dispose(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reentrant combined loader disposal deadlocked")
	}
	if err := loaded.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("disposal calls = %d, want one for the single loaded generation", calls)
	}
}
