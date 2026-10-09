package coding

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8964-extension-provider-streaming.test.ts:7 (stream and streamSimple), through separate producer and consumer extension processes.
func TestExtensionProviderCommandStreamsAcrossProcesses(t *testing.T) {
	services := newTestServices(t)
	session, err := NewSession(services, SessionOptions{Model: &ai.Model{ID: "primary", Provider: &scriptedProvider{}}, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	bridge := subprocess.NewUIBridge(func() {})
	detach := icodingagent.WireModelOperations(bridge, icodingagent.ModelOperationBindings{CurrentModel: session.Model, ModelLookup: services.ModelRuntime().GetModel, ModelCatalog: func(...string) []*ai.Model { return services.ModelRuntime().GetModels() }, Registry: services.Registry().ModelRegistry, ModelBuilder: func(spec string) (*ai.Model, error) { return BuildModel(spec, services) }, SessionHandle: session})
	defer detach()
	host.SetUIBridge(bridge)
	_, err = host.Load(t.Context(), subprocess.ExtConfig{Name: "provider-stream-producer", Source: filepath.Join("testdata", "provider-stream-producer.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "provider-stream-consumer", Source: filepath.Join("testdata", "provider-stream-consumer.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"stream", "streamSimple"} {
		t.Run(method, func(t *testing.T) {
			if err := consumer.Commands["stream-custom"].Handler(t.Context(), method); err != nil {
				t.Fatal(err)
			}
		})
	}
}
