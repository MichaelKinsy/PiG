package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestModelCatalogBindingReusesFirstEncoding(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	catalog := registry.GetAllModelData()
	projection := testing.AllocsPerRun(20, func() {
		for _, model := range catalog {
			_ = extension.ModelInfo(model)
		}
	})
	measure := func(legacy bool) int64 {
		return testing.Benchmark(func(b *testing.B) {
			for b.Loop() {
				captureModelPublications(b, legacy, registry.GetAllModelData, registry)
			}
		}).AllocsPerOp()
	}
	legacy, encoded := measure(true), measure(false)
	if float64(legacy-encoded) < projection {
		t.Fatalf("binding saved %d allocations, less than the eliminated model projection (%.0f)", legacy-encoded, projection)
	}
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
