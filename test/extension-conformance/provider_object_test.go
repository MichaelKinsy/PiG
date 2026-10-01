package extensionconformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
					testProviderObjectPair(t, root, role, language, placement)
				})
			}
		}
	}
}

func testProviderObjectPair(t *testing.T, root, role, language, placement string) {
	t.Helper()
	dir := t.TempDir()
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(dir)
	t.Cleanup(func() { host.Shutdown("done") })
	host.SetProviderCallbacks(services.Registry().RegisterProvider, services.Registry().UnregisterProvider)
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
		t.Fatal("native provider model missing from host runtime")
	}
	stream := services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("host turn")}}}, ai.StreamOptions{})
	message := stream.Result()
	if message.StopReason != ai.StopReasonStop {
		t.Fatalf("host native stream: %+v", message)
	}
	if len(message.Content) != 1 || message.Content[0].(ai.TextContent).Text != "simple" {
		t.Fatalf("host native result: %+v", message)
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
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(dir)
	t.Cleanup(func() { host.Shutdown("done") })
	host.SetProviderCallbacks(services.Registry().RegisterProvider, services.Registry().UnregisterProvider)
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
