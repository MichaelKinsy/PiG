package coding

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// TestNodeConfigProviderRootAcrossCellMembers drives two ordinary Node factories in one cell through the production host and Model Runtime. Pi 0.87.1 keeps one effective registered root per provider (model-runtime.ts:438-443,753-766), passes it as streamSimple's receiver (provider-composer.ts:500-501), and a later registrant owns the merged registration.
func TestNodeConfigProviderRootAcrossCellMembers(t *testing.T) {
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
	author, err := filepath.Abs(filepath.Join("testdata", "provider-config-root-author.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := filepath.Abs(filepath.Join("testdata", "provider-config-root-reader.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	exts, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{
		{Name: "config-root-author", Source: author, Enabled: true},
		{Name: "config-root-reader", Source: reader, Enabled: true},
	})
	if len(errs) != 0 || len(exts) != 2 {
		t.Fatalf("LoadAll = %d extensions, %v", len(exts), errs)
	}
	if err := exts[0].Commands["root-check"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if err := exts[1].Commands["root-reader-check"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if got := host.ProviderNames("config-root-author"); slices.Contains(got, "config-root") {
		t.Fatalf("the superseded author still owns the provider's cleanup: %v", got)
	}
	if got := host.ProviderNames("config-root-reader"); !slices.Contains(got, "config-root") {
		t.Fatalf("the later registrant does not own the provider: %v", got)
	}
}
