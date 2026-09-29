package codingagent

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func BenchmarkModelCommandCachedMatch(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			registry := NewModelRegistry(b.TempDir())
			models := make([]extension.ProviderModelConfig, count)
			for i := range models {
				models[i] = extension.ProviderModelConfig{ID: fmt.Sprintf("cached-%d", i)}
			}
			if err := registry.RegisterProvider("bench", extension.ProviderConfig{API: ai.APIOpenAICompletions, APIKey: "key", BaseURL: "http://localhost:1", Models: models}); err != nil {
				b.Fatal(err)
			}
			m := NewInteractiveMode(InteractiveOptions{ModelRegistry: registry})
			b.ReportAllocs()
			for b.Loop() {
				called := false
				m.findExactModelMatch(b.Context(), "bench/cached-0", func(spec string, ok bool) {
					called = true
					if !ok || spec != "bench/cached-0" {
						b.Fatalf("match=%s,%v", spec, ok)
					}
				})
				if !called {
					b.Fatal("cache lookup started background work")
				}
			}
		})
	}
}
