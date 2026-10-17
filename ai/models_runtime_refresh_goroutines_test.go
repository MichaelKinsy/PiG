package ai

import (
	"fmt"
	"runtime/metrics"
	"testing"
)

// goroutinesCreated reads the process's goroutine creation count.
func goroutinesCreated() uint64 {
	sample := []metrics.Sample{{Name: "/sched/goroutines-created:goroutines"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

// An offline catalog restore of the 41 built-in remote catalogs ran three goroutines per provider: a waiter, the operation it awaited, and the
// publication its Update queued. The operation now runs on the waiter's goroutine slot and settles through its signal, and a publication with
// nothing to wait for and nothing to write runs on the caller, as Pi's does on an already-settled chain (models.ts publishProviderModels).
func TestModelsOfflineRefreshStartsOneGoroutinePerProvider(t *testing.T) {
	const providers = 40
	models := CreateModels()
	t.Cleanup(models.Close)
	published := 0
	for i := range providers {
		id := fmt.Sprintf("dyn-%d", i)
		models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: id, refreshModels: func(refresh RefreshModelsContext) error {
			ok, err := refresh.Publish(ModelsPublication{Update: func() { published++ }})
			if !ok && err == nil {
				t.Errorf("%s: publication was superseded", id)
			}
			return err
		}}))
	}
	before := goroutinesCreated()
	result := models.Refresh(t.Context(), ModelsRefreshOptions{AllowNetwork: new(false)})
	created := goroutinesCreated() - before
	if result.Aborted || len(result.Errors) != 0 || published != providers {
		t.Fatalf("refresh = %+v, %d of %d published", result, published, providers)
	}
	// One per provider, plus the refresh's own join.
	if created > providers+4 {
		t.Fatalf("an offline refresh of %d providers started %d goroutines, want at most %d", providers, created, providers+4)
	}
}
