package extensionconformance

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// BenchmarkModelRegistryOverRuntime measures the registry facade of an extension's own ModelRuntime against the real host and SDK codec (in process, no socket): "read" is one facade read of a builtin provider's display name (one runtime call), "native" is one registerNativeProvider with the host's registration refresh, the two lookups of the object and the removal. The extension's loop runs b.N times inside one tool call, so the figures are per operation without the tool dispatch.
func BenchmarkModelRegistryOverRuntime(b *testing.B) {
	for _, mode := range []string{"read", "native"} {
		b.Run(mode, func(b *testing.B) {
			host := subprocess.NewHostWithConfigRoot(b.TempDir(), b.TempDir())
			b.Cleanup(func() { host.Shutdown("benchmark complete") })
			host.SetSessionGraph(coding.NewSessionGraph())
			authPath := filepath.Join(b.TempDir(), "auth.json")
			ext := sdk.New("registry-bench")
			ext.RegisterTool(sdk.ToolDefinition{
				Name: "bench", Label: "bench", Description: "Runs the facade loop.", Parameters: sdk.Schema{"type": "object"},
				Execute: func(ctx sdk.Context, args map[string]any) (any, error) {
					refresh := false
					rt, err := sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: authPath, NoModelsFile: true, RefreshOnCreate: &refresh})
					if err != nil {
						return nil, err
					}
					registry := sdk.NewModelRegistry(rt)
					provider, err := sdk.CreateProvider(sdk.CreateProviderOptions{
						ID: "bench-prov",
						Auth: sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{Name: "Key", Resolve: func(sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
							return &sdk.AuthResult{Auth: map[string]any{"apiKey": "k"}}, nil
						}}},
						Models: []map[string]any{{"id": "m", "name": "m", "api": "bench-api", "provider": "bench-prov"}},
						APIs:   map[string]sdk.ProviderStreams{"bench-api": {Stream: unusedBenchStream}},
					})
					if err != nil {
						return nil, err
					}
					n := int(args["n"].(float64))
					for range n {
						if args["mode"] == "read" {
							if _, err := registry.GetProviderDisplayName("anthropic"); err != nil {
								return nil, err
							}
							continue
						}
						if err := registry.RegisterNativeProvider(provider); err != nil {
							return nil, err
						}
						if found, err := registry.GetRegisteredNativeProvider("bench-prov"); err != nil || found == nil {
							return nil, fmt.Errorf("registered provider missing: %w", err)
						}
						if found, err := registry.GetProvider("bench-prov"); err != nil || found == nil {
							return nil, fmt.Errorf("provider missing: %w", err)
						}
						if err := registry.UnregisterProvider("bench-prov"); err != nil {
							return nil, err
						}
					}
					return "done", nil
				},
			})
			loaded, err := host.LoadInProcess(b.Context(), subprocess.ExtConfig{Name: "registry-bench", Enabled: true}, ext.RunWithConn)
			if err != nil {
				b.Fatal(err)
			}
			runner := inproc.NewRunner([]extension.Extension{*loaded}, b.TempDir(), host.Runtime())
			runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
			b.Cleanup(func() { runner.Invalidate("benchmark complete") })
			encoded, err := json.Marshal(map[string]any{"n": b.N, "mode": mode})
			if err != nil {
				b.Fatal(err)
			}
			def := loaded.Tools["bench"]
			b.ReportAllocs()
			b.ResetTimer()
			if _, err := def.Definition.Execute(b.Context(), "call-bench", encoded, nil); err != nil {
				b.Fatal(err)
			}
		})
	}
}

func unusedBenchStream(map[string]any, map[string]any, sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) {
	return nil, errors.New("unused")
}
