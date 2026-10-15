package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: packages/coding-agent/src/core/model-registry.ts:64 (ModelRegistry.getAll).
func TestModelCatalogBindingPublishesTheFinishedBindingOnce(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	catalog := registry.GetAllModelData()
	projection := testing.AllocsPerRun(20, func() {
		for _, model := range catalog {
			_, _ = extension.ModelObject(model)
		}
	})
	measure := func(unbatched bool) int64 {
		return testing.Benchmark(func(b *testing.B) {
			for b.Loop() {
				captureModelPublicationsVia(b, false, unbatched, registry.GetAllModelData, registry)
			}
		}).AllocsPerOp()
	}
	// Batching sends one snapshot of the finished binding instead of three; each skipped snapshot assembles the registry state again.
	unbatched, batched := measure(true), measure(false)
	if batched >= unbatched {
		t.Fatalf("batched binding allocates %d per wiring, unbatched %d; want fewer", batched, unbatched)
	}
	t.Logf("unbatched %d, batched %d allocations per wiring; one model projection %.0f", unbatched, batched, projection)
}

func BenchmarkWireModelCatalogBinding(b *testing.B) {
	for _, legacy := range []bool{true, false} {
		name := "encoded"
		if legacy {
			name = "legacy"
		}
		b.Run(name, func(b *testing.B) {
			registry := NewModelRegistry(b.TempDir())
			b.ReportAllocs()
			for b.Loop() {
				captureModelPublications(b, legacy, registry.GetAllModelData, registry)
			}
		})
	}
}
