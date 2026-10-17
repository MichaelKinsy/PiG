package subprocess_test

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Measures native auth plus streamed partial/terminal delivery through a real
// Node process and the same ModelRuntime used by production Session callers.
func BenchmarkNativeProviderStream(b *testing.B) {
	dir := b.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(services.Close)
	host := subprocess.NewHostWithConfigRoot(dir, dir)
	defer host.Shutdown("benchmark done")
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	host.SetUIBridge(subprocess.NewUIBridge(nil))
	path, err := filepath.Abs("../../../../test/parity/scenarios/testdata/extension-native-provider.mjs")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := host.Load(b.Context(), subprocess.ExtConfig{Name: "extension-native-provider", Source: path, Enabled: true}); err != nil {
		b.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("parity-native", "native-model")
	if model == nil {
		b.Fatal("native model absent")
	}
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("benchmark")}}}
	b.ReportAllocs()
	for b.Loop() {
		stream := services.ModelRuntime().StreamSimple(b.Context(), model, request, ai.StreamOptions{})
		for range stream.Events(b.Context()) {
		}
		if result := stream.Result(); result.StopReason != ai.StopReasonStop {
			b.Fatalf("native result: %+v", result)
		}
	}
}
