package coding

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// registerLoggedNativeProviders registers native providers whose catalog callbacks record their provider ID, and returns the ordered log.
func registerLoggedNativeProviders(t *testing.T, runtime *ModelRuntime, ids ...string) func() []string {
	t.Helper()
	var mu sync.Mutex
	var log []string
	for _, id := range ids {
		provider := &ai.ModelsProvider{ID: id, GetModels: func() ([]*ai.Model, error) {
			mu.Lock()
			log = append(log, id)
			mu.Unlock()
			return []*ai.Model{nativeCompatModel("m", id, "https://native.invalid/v1")}, nil
		}}
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(log)
	}
}

// Pi's updateModelSnapshot and refresh read every provider's catalog through getModels, which runs a provider's own callback. A full availability pass and a registration snapshot must still run each native callback, in provider order, although they compose no unused static catalog.
func TestFullAvailabilityPassRunsNativeCatalogCallbacksInProviderOrder(t *testing.T) {
	services := registrationServices(t, nil)
	runtime := services.ModelRuntime()
	log := registerLoggedNativeProviders(t, runtime, "zz-native-second", "aa-native-first")
	if _, err := runtime.GetAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := len(log())
	if _, err := runtime.GetAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	pass := log()[before:]
	firstRead := map[string]int{}
	for i, id := range pass {
		if _, seen := firstRead[id]; !seen {
			firstRead[id] = i
		}
	}
	for _, id := range []string{"zz-native-second", "aa-native-first"} {
		if _, ok := firstRead[id]; !ok {
			t.Fatalf("full pass did not run %s's catalog callback; log %v", id, pass)
		}
	}
	// Registration order is the provider order of native providers.
	if firstRead["zz-native-second"] > firstRead["aa-native-first"] {
		t.Fatalf("native catalog callbacks ran out of provider order: %v", pass)
	}
}

// clearProviderEnvKeys unsets every environment API key of the built-in providers, so a developer's shell cannot configure a provider the test expects to be unconfigured.
func clearProviderEnvKeys(t *testing.T) {
	t.Helper()
	for _, provider := range ai.ListProviders() {
		for _, name := range ai.FindEnvKeys(provider, nil) {
			t.Setenv(name, "")
		}
	}
}

// A registration snapshot publishes the models of every configured provider, including a static built-in provider whose catalog it composes only after learning that the provider is configured.
func TestRegistrationSnapshotPublishesConfiguredStaticProviderCatalog(t *testing.T) {
	clearProviderEnvKeys(t)
	services := registrationServices(t, map[string]ai.Credential{"anthropic": {Type: ai.CredentialAPIKey, Key: "stored"}})
	runtime := services.ModelRuntime()
	if _, err := runtime.GetAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterNativeProvider(&ai.ModelsProvider{ID: "snapshot-native", GetModels: func() ([]*ai.Model, error) {
		return []*ai.Model{nativeCompatModel("m", "snapshot-native", "https://native.invalid/v1")}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.GetAvailableSnapshot()
	if snapshot == nil {
		t.Fatal("snapshot is nil, want an empty or populated list")
	}
	want := len(ai.ListModels("anthropic"))
	if got := len(providerModelIDs(snapshot, "anthropic")); got != want || want == 0 {
		t.Fatalf("configured static provider published %d models, want %d", got, want)
	}
	if got := providerModelIDs(snapshot, "openai"); len(got) != 0 {
		t.Fatalf("unconfigured provider published %v", got)
	}
}

// An unconfigured deployment has no static catalog to publish, so a full pass must not compose the built-in catalog only to drop it. GetModels composes it for a caller that asked for it.
func TestFullAvailabilityPassDoesNotComposeUnconfiguredStaticCatalog(t *testing.T) {
	clearProviderEnvKeys(t)
	services := registrationServices(t, nil)
	runtime := services.ModelRuntime()
	ctx := context.Background()
	if _, err := runtime.GetAvailable(ctx); err != nil {
		t.Fatal(err)
	}
	catalog := testing.AllocsPerRun(5, func() { _ = runtime.GetModels() })
	pass := testing.AllocsPerRun(5, func() {
		if err := runtime.queueAvailabilityRefresh(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if pass > catalog/2 {
		t.Fatalf("a full pass allocates %.0f times, more than half of the %.0f allocations that composing the catalog takes", pass, catalog)
	}
}

// Pi's registerProvider and unregisterProvider run updateModelSnapshot, which publishes only configured providers' models. With no provider configured, a registration snapshot over the built-in provider order must not compose the built-in catalog only to drop it.
func TestRegistrationSnapshotDoesNotComposeUnconfiguredStaticCatalog(t *testing.T) {
	clearProviderEnvKeys(t)
	services := registrationServices(t, nil)
	runtime := services.ModelRuntime()
	if _, err := runtime.GetAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	catalog := testing.AllocsPerRun(5, func() { _ = runtime.GetModels() })
	projection := testing.AllocsPerRun(5, func() { runtime.updateModelSnapshot("unregistered-provider", nil, ai.ListProviders) })
	if projection > catalog/2 {
		t.Fatalf("a registration snapshot allocates %.0f times, more than half of the %.0f allocations that composing the catalog takes", projection, catalog)
	}
	if got := runtime.GetAvailableSnapshot(); len(got) != 0 {
		t.Fatalf("unconfigured deployment published %d models", len(got))
	}
}
