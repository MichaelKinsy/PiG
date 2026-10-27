package sdk

import "testing"

// BenchmarkCreateProviderDispatch measures what the provider CreateProvider builds adds to one stream call: the dispatch on model.api (a map lookup) in front of the implementation, against calling the implementation directly.
func BenchmarkCreateProviderDispatch(b *testing.B) {
	model := map[string]any{"id": "m", "api": "api-b", "provider": "p"}
	implementation := func(map[string]any, map[string]any, ProviderStreamOptions) (*ModelEventStream, error) {
		return nil, nil
	}
	streams := ProviderStreams{Stream: implementation, StreamSimple: implementation}
	provider, err := CreateProvider(CreateProviderOptions{ID: "p", Models: nil, APIs: map[string]ProviderStreams{"api-a": streams, "api-b": streams, "api-c": streams}})
	if err != nil {
		b.Fatal(err)
	}
	b.Run("direct", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = implementation(model, nil, ProviderStreamOptions{})
		}
	})
	b.Run("dispatch", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = provider.Stream(model, nil, ProviderStreamOptions{})
		}
	})
	b.Run("models", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = provider.GetModels()
		}
	})
}
