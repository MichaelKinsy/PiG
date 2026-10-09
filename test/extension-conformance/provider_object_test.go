package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/providerobject"
)

// Pi model-registry.ts:101-103,165-167 returns the original Provider object, including callback-bearing auth and refresh methods.
func TestNodeProviderObjectCarrier(t *testing.T) {
	testNodeProviderObjectCarrier(t, false)
}

func TestNodeRemoteProviderObjectCarrier(t *testing.T) {
	testNodeProviderObjectCarrier(t, true)
}

// Pi packages/coding-agent/src/core/extensions/types.ts:1830-1831 declares registerProvider(provider: Provider) beside registerProvider(name, config): the
// Provider-object overload. Every SDK registers a callback-bearing Provider (Go RegisterNativeProvider, Python register_native_provider, Rust
// register_native_provider) from an extension subprocess through the host wire; the production host registry then holds its model and streams a
// host turn through the owner's callbacks, and a second extension reads the same Provider back (model-registry.ts:101-103,165-167).
// mutation-checked: Extension.RegisterNativeProvider in extensions/sdk/provider.go not queueing the declaration fails the go subtests (model missing from the host runtime).
func TestProviderObjectsAcrossSDKs(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"go", "python", "rust"} {
		placements := []string{"strict", "packed"}
		if language == "go" {
			placements = append(placements, "fused")
		}
		for _, placement := range placements {
			for _, role := range []string{"owner", "reader"} {
				t.Run(language+"-"+role+"-"+placement, func(t *testing.T) {
					t.Setenv("CARRIER_ROLE", role)
					testProviderObjectPair(t, root, role, language, placement, "pi.registerProvider(provider)")
				})
			}
		}
	}
}

func testProviderObjectPair(t *testing.T, root, role, language, placement, call string) {
	t.Helper()
	dir := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(dir)
	t.Cleanup(func() { host.Shutdown("done") })
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	host.SetUIBridge(subprocess.NewUIBridge(nil))
	owner := subprocess.ExtConfig{Name: "extension-provider-carrier-owner", Source: filepath.Join(root, "test/parity/scenarios/testdata/extension-provider-carrier-owner.mjs"), Enabled: true, Isolation: "strict"}
	reader := subprocess.ExtConfig{Name: "extension-provider-carrier-remote", Source: filepath.Join(root, "test/parity/scenarios/testdata/extension-provider-carrier-remote.mjs"), Enabled: true, Isolation: "strict"}
	native := subprocess.ExtConfig{Name: "provider-object-" + language, Source: filepath.Join(root, "test/extension-conformance/testdata/provider-object-"+language), Enabled: true, Isolation: "strict", RuntimeKind: "subprocess", RuntimeLanguage: language, EntrypointKind: "factory"}
	switch language {
	case "go":
		native.Factory = "Extension"
		native.SDKName = "github.com/MichaelKinsy/PiG/extensions/sdk"
		native.ModulePath = "example.com/provider-object-go"
		native.Package = native.ModulePath
	case "python":
		native.Factory = "new_extension"
		native.SDKName = "pig-sdk-py"
		native.Package = "provider_object"
	case "rust":
		native.Factory = "new_extension"
		native.SDKName = "pig-sdk"
		native.Package = "provider-object-rust"
	}
	if placement == "packed" {
		native.Isolation = "shared-ok"
	}
	if role == "owner" {
		owner = native
	} else {
		reader = native
	}
	configs := []subprocess.ExtConfig{owner, reader}
	if placement == "packed" {
		peer := providerPeer(t, root, native)
		if role == "owner" {
			configs = []subprocess.ExtConfig{owner, peer, reader}
		} else {
			configs = append(configs, peer)
		}
		cells := subprocess.PlanCells(configs, nil)
		packed := false
		for _, cell := range cells {
			if len(cell.Extensions) == 2 {
				packed = true
			}
		}
		if !packed {
			t.Fatalf("native peer was not packed: %+v", cells)
		}
	}
	var loaded []extension.Extension
	if placement == "fused" {
		for _, config := range configs {
			var result *extension.Extension
			var err error
			if config.Name == native.Name {
				result, err = host.LoadInProcess(t.Context(), config, providerobject.Extension().RunWithConn)
			} else {
				result, err = host.Load(t.Context(), config)
			}
			if err != nil {
				t.Fatal(err)
			}
			loaded = append(loaded, *result)
		}
	} else {
		var errs []error
		loaded, errs = host.LoadAll(t.Context(), configs)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
	}
	if len(loaded) != len(configs) {
		t.Fatalf("loaded %d of %d configured providers", len(loaded), len(configs))
	}
	model := services.ModelRuntime().GetModel("carrier-provider", "carrier-model")
	if model == nil {
		t.Fatalf("%s: native provider model missing from host runtime", call)
	}
	// upstream: coding-agent extensions/types.ts:1917-1920 (a provider's streamSimple invokes options.onPayload and options.onResponse) and sdk.ts:387-433
	// (the caller's onResponse is handleProviderResponse). The values below come only from the extension's own calls, so a host or SDK that
	// never delivers the hooks cannot satisfy this row.
	var hookMu sync.Mutex
	var payloads []any
	var responses []ai.ProviderResponse
	stream := services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("host turn")}}}, ai.StreamOptions{
		OnPayload: func(payload any, _ *ai.Model) (any, error) {
			hookMu.Lock()
			defer hookMu.Unlock()
			payloads = append(payloads, payload)
			return nil, nil
		},
		OnResponse: func(_ context.Context, response ai.ProviderResponse, _ *ai.Model) error {
			hookMu.Lock()
			defer hookMu.Unlock()
			responses = append(responses, response)
			return nil
		},
	})
	message := stream.Result()
	// Only the owner role streams through the SDK under test; the reader role's provider is the Node carrier.
	if role == "owner" {
		hookMu.Lock()
		if len(payloads) != 1 || fmt.Sprint(payloads[0]) != "map[marker:carrier-payload]" {
			t.Errorf("the caller's payload hook saw %v, want one carrier payload", payloads)
		}
		if len(responses) != 1 || responses[0].Status != 207 || responses[0].Headers["x-carrier"] != "carrier-response" {
			t.Errorf("the caller's response hook saw %+v, want one 207 response with x-carrier: carrier-response", responses)
		}
		hookMu.Unlock()
	}
	if message.StopReason != ai.StopReasonStop {
		t.Fatalf("host native stream: %+v", message)
	}
	if len(message.Content) != 1 || message.Content[0].(ai.TextContent).Text != "simple" {
		t.Fatalf("host native result: %+v", message)
	}
	// upstream: models.ts Provider.fetchDeferred / cancelDeferred through Models.fetchDeferred / cancelDeferred: the host's own runtime reaches the extension process's deferred operations, with the handle the caller passed.
	handle := ai.DeferredHandle{Provider: "carrier-provider", ModelID: "carrier-model", API: model.ProviderMeta.API, ID: "deferred"}
	fetched := services.ModelRuntime().FetchDeferred(t.Context(), model, handle)
	if fetched.StopReason != ai.StopReasonStop || len(fetched.Content) != 1 || fetched.Content[0].(ai.TextContent).Text != "deferred" {
		t.Fatalf("host deferred fetch: %+v", fetched)
	}
	if err := services.ModelRuntime().CancelDeferred(t.Context(), model, handle); err != nil {
		t.Fatalf("host deferred cancel: %v", err)
	}
	wrong := handle
	wrong.ID = "other"
	if err := services.ModelRuntime().CancelDeferred(t.Context(), model, wrong); err == nil {
		t.Fatal("host deferred cancel of another handle succeeded, want the extension's rejection")
	}
	// upstream: models.ts Provider.getAllModels / filterAllModels: the all-types catalog is the provider's getAllModels (it lists a model getModels does not), and the credential-specific availability across every model type is its filterAllModels, not filterModels (which, for this credential, shows none).
	if services.ModelRuntime().GetModel("carrier-provider", "carrier-all-only") == nil {
		t.Fatal("the model only getAllModels lists is missing from the host runtime")
	}
	if err := services.Auth().Set("carrier-provider", ai.Credential{Type: ai.CredentialAPIKey, Key: "all"}); err != nil {
		t.Fatal(err)
	}
	services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{"carrier-provider"}})
	available, err := services.ModelRuntime().GetAvailable(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var availableIDs []string
	for _, candidate := range available {
		if candidate.ProviderMeta.ProviderID == "carrier-provider" {
			availableIDs = append(availableIDs, candidate.ID)
		}
	}
	if !slices.Contains(availableIDs, "carrier-all-only") || !slices.Contains(availableIDs, "carrier-model") {
		t.Fatalf("available carrier models = %v, want filterAllModels' result (every model for the all credential)", availableIDs)
	}
	path := filepath.Join(dir, "result.json")
	for _, item := range loaded {
		if item.Name == reader.Name {
			if err := item.Commands["remote-carrier-probe"].Handler(t.Context(), path); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"source":"injected:CARRIER_SOURCE"`, `"apiKey":"injected:CARRIER_KEY"`, `"key":"Carrier key"`, `"access":"rotated"`, `"persist","update"`, `"carrier-model","refreshed"`, `"text":"carrier answer"`, `"text":"simple"`, `"text":"deferred"`, `"cancelled":"aborted"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %s in %s", want, data)
		}
	}
}

func providerPeer(t *testing.T, root string, native subprocess.ExtConfig) subprocess.ExtConfig {
	t.Helper()
	peer := native
	peer.Name = "provider-peer"
	// Owner and reader roles use the same peer source and artifact, but start fresh cells.
	peer.Source = filepath.Join(fixtureRoot, "provider-peer-"+native.RuntimeLanguage)
	write := func(path, content string) {
		t.Helper()
		target := filepath.Join(peer.Source, path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	switch native.RuntimeLanguage {
	case "go":
		peer.ModulePath = "example.com/provider-peer"
		peer.Package = peer.ModulePath
		write("go.mod", fmt.Sprintf("module %s\n\ngo 1.26.0\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v%s\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", peer.ModulePath, pigversion.PigVersion, filepath.ToSlash(filepath.Join(root, "extensions/sdk"))))
		write("extension.go", "package peer\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension()*sdk.Extension{return sdk.New(\"provider-peer\")}\n")
	case "python":
		peer.Package = "provider_peer"
		write("provider_peer.py", "import pig_sdk\ndef new_extension():\n    return pig_sdk.Extension('provider-peer')\n")
	case "rust":
		peer.Package = "provider-peer"
		write("Cargo.toml", fmt.Sprintf("[package]\nname=\"provider-peer\"\nversion=\"0.1.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk={path=%q}\n", filepath.ToSlash(filepath.Join(root, "extensions/sdk-rs"))))
		write("src/lib.rs", "pub fn new_extension()->pig_sdk::Extension{pig_sdk::Extension::new(\"provider-peer\")}\n")
	}
	return peer
}

func testNodeProviderObjectCarrier(t *testing.T, remote bool) {
	t.Helper()
	dir := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(dir)
	t.Cleanup(func() { host.Shutdown("done") })
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	host.SetUIBridge(subprocess.NewUIBridge(nil))
	var configs []subprocess.ExtConfig
	reader, isolation, command := "reader", "", "carrier-probe"
	if remote {
		reader, isolation, command = "remote", "strict", "remote-carrier-probe"
	}
	for _, suffix := range []string{"owner", reader} {
		name := "extension-provider-carrier-" + suffix
		source, err := filepath.Abs(filepath.Join("../parity/scenarios/testdata", name+".mjs"))
		if err != nil {
			t.Fatal(err)
		}
		configs = append(configs, subprocess.ExtConfig{Name: name, Source: source, Enabled: true, Isolation: isolation})
	}
	loaded, errs := host.LoadAll(t.Context(), configs)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(loaded) != len(configs) {
		t.Fatalf("loaded %d extensions for %d configured sources", len(loaded), len(configs))
	}
	path := filepath.Join(dir, "carrier.json")
	if err := loaded[1].Commands[command].Handler(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("invalid result: %s", data)
	}
	// These values come from the caller's closures, not host auth or metadata snapshots.
	if !remote && !strings.Contains(string(data), `"identity":true`) {
		t.Fatalf("same-cell identity missing: %s", data)
	}
	for _, value := range []string{`"source":"injected:CARRIER_SOURCE"`, `"apiKey":"injected:CARRIER_KEY"`, `"key":"Carrier key"`, `"access":"rotated"`, `"persist","update"`, `"carrier-model","refreshed"`, `"text":"carrier answer"`, `"text":"simple"`, `"text":"deferred"`, `"cancelled":"aborted"`} {
		if !strings.Contains(string(data), value) {
			t.Errorf("missing %s in %s", value, data)
		}
	}
	if remote {
		if err := loaded[1].Commands["carrier-capture"].Handler(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		if err := loaded[0].Commands["carrier-remove"].Handler(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		if services.ModelRuntime().GetModel("carrier-provider", "carrier-model") != nil {
			t.Fatal("unregister kept the catalog model")
		}
		retained := filepath.Join(dir, "retained.json")
		if err := loaded[1].Commands["carrier-retained"].Handler(t.Context(), retained); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(retained)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `[{"type":"text","text":"simple"}]` {
			t.Fatalf("retained Provider result: %s", body)
		}
		if err := loaded[0].Commands["carrier-crash"].Handler(t.Context(), ""); err == nil {
			t.Fatal("owner crash reported success")
		}
		if err := loaded[1].Commands["carrier-retained"].Handler(t.Context(), retained); err == nil {
			t.Fatal("captured Provider survived its connection")
		}
	}
}
