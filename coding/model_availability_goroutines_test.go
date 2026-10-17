package coding

import (
	"context"
	"runtime/metrics"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func goroutinesCreated() uint64 {
	sample := []metrics.Sample{{Name: "/sched/goroutines-created:goroutines"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

// pig -p runs two offline model passes (lever-RegistryAuth Q1), and each started about 227 goroutines: three per remote catalog in the restore and
// one per provider, twice, in the availability pass. A pass that runs no extension code now runs its providers on the caller, in provider order.
func TestOfflineModelPassesRunProvidersOnTheCaller(t *testing.T) {
	clearProviderEnvKeys(t)
	services := registrationServices(t, map[string]ai.Credential{"anthropic": {Type: ai.CredentialAPIKey, Key: "stored"}})
	runtime := services.ModelRuntime()
	ctx := context.Background()
	refresh := func() {
		if result := runtime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted || len(result.Errors) != 0 {
			t.Fatalf("refresh = %+v", result)
		}
	}
	refresh()
	before := goroutinesCreated()
	if err := runtime.queueAvailabilityRefresh(ctx); err != nil {
		t.Fatal(err)
	}
	pass := goroutinesCreated() - before
	before = goroutinesCreated()
	refresh()
	restore := goroutinesCreated() - before
	t.Logf("availability pass: %d goroutines; offline refresh (restore and pass): %d", pass, restore)
	if pass > 8 {
		t.Errorf("an availability pass started %d goroutines, want at most 8", pass)
	}
	if restore > 40 {
		t.Errorf("an offline refresh started %d goroutines, want at most 40", restore)
	}
	if !runtime.HasConfiguredAuth("anthropic") || len(providerModelIDs(runtime.GetAvailableSnapshot(), "anthropic")) == 0 {
		t.Fatal("the passes did not publish the stored credential's provider")
	}
}
