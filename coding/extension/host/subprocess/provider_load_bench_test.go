package subprocess

import (
	"fmt"
	"net"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// BenchmarkLoadInProcessProviders is the cost of loading one extension that registers providers while it loads into a host bound to a registry beforehand, as the CLI binds its host, followed by the bind that applies what the extension queued (agent-session-services.ts:157-169): each op is one host, one load and one apply, and the registry accepts every registration.
func BenchmarkLoadInProcessProviders(b *testing.B) {
	for _, providers := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("providers=%d", providers), func(b *testing.B) {
			ext := sdk.New("provider-load-bench")
			for i := range providers {
				ext.RegisterProvider(fmt.Sprintf("bench-%d", i), sdk.ProviderConfig{"api": "openai-completions", "baseUrl": "https://bench.invalid/v1", "apiKey": "key", "models": []any{map[string]any{"id": "m", "name": "M"}}})
			}
			b.ReportAllocs()
			for b.Loop() {
				host := NewHost(b.TempDir())
				registered := 0
				host.SetProviderCallbacks(func(string, extension.ProviderConfig) error { registered++; return nil }, func(string) {})
				host.SetUIBridge(NewUIBridge(func() {}))
				// One Extension serves one connection at a time: the next iteration serves it again only after this serve returned.
				served := make(chan struct{})
				serve := func(conn net.Conn) error {
					defer close(served)
					return ext.RunWithConn(conn)
				}
				if _, err := host.LoadInProcess(b.Context(), ExtConfig{Name: "provider-load-bench", Enabled: true}, serve); err != nil {
					b.Fatal(err)
				}
				runtime := host.Runtime()
				runtime.BindProviderActions(runtime.BoundProviderActions(), nil)
				if registered != providers {
					b.Fatalf("registry received %d registrations, want %d", registered, providers)
				}
				host.Shutdown("benchmark iteration")
				<-served
			}
		})
	}
}
