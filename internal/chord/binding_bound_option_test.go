package chord

import (
	"context"
	"testing"
)

// packages/chord/src/types.ts:272 `readonly bound?: boolean` and packages/chord/src/services/consumer.ts:464
// `this.#bound = options.bound ?? true`: an omitted or true Bound starts the binding bound, so use() hydrates the singleton
// at once (consumer.ts:483), and false leaves it cold until rebind(true).
func TestRemoteServiceBindingBoundOptionDefaultsToBound(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		bound    *bool
		hydrated bool
	}{
		{"bound omitted", nil, true},
		{"bound true", new(true), true},
		{"bound false", new(false), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
			provideModels(t, provider, newModels(t, 4))
			binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: ServiceIDs(modelsDefinition.Id()), Bound: tc.bound})
			replica := modelsReplica(t, use(t, binding, modelsDefinition.Id()))
			// Ready waits for the singleton hydration a bound binding starts, so a false Bound that still started one fails here.
			if err := binding.Ready(ctx); err != nil {
				t.Fatal(err)
			}
			revision, hydrated := revisionOf(t, replica)
			if hydrated != tc.hydrated || hydrated && revision != 4 {
				t.Fatalf("Bound %v: hydrated = %v revision = %d, want hydrated %v", tc.bound, hydrated, revision, tc.hydrated)
			}
			if !tc.hydrated {
				if err := binding.Rebind(ctx, true); err != nil {
					t.Fatal(err)
				}
				if revision, hydrated := revisionOf(t, replica); !hydrated || revision != 4 {
					t.Fatalf("after Rebind(true): hydrated = %v revision = %d", hydrated, revision)
				}
			}
			_ = binding.Dispose(ctx)
			_ = provider.Dispose()
		})
	}
}
