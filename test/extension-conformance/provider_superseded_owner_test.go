package extensionconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// A provider_superseded notification ends an owner's claim to the provider's root, not the life of a Provider object another extension captured. Pi
// keeps the captured object callable (model-runtime.ts:744-750 retains the supplied Provider; registerProvider and unregisterProvider replace the
// effective registration, not the object a caller holds), and the host keeps the captured handle routed to its owner until the last reference goes
// (provider_object_lifetime.go collectProviderObjectLocked, then provider_release). The Node runtime's provider_superseded handler forgets the
// ownership tables (runtime.mjs forgetProviderOwnership) and keeps nativeProviderCallbacks, which only provider_release drops. Every SDK must do
// the same: a later extension registering the provider's root at load supersedes the native owner, and the reader's captured Provider still streams.
func TestSupersededOwnerKeepsACapturedProviderCallableAcrossSDKs(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"node", "go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
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
			switch language {
			case "go":
				owner = subprocess.ExtConfig{Name: "provider-object-go", Source: filepath.Join(root, "test/extension-conformance/testdata/provider-object-go"), Enabled: true, Isolation: "strict", RuntimeKind: "subprocess", RuntimeLanguage: language, EntrypointKind: "factory", Factory: "Extension", SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/provider-object-go", Package: "example.com/provider-object-go"}
			case "python":
				owner = subprocess.ExtConfig{Name: "provider-object-python", Source: filepath.Join(root, "test/extension-conformance/testdata/provider-object-python"), Enabled: true, Isolation: "strict", RuntimeKind: "subprocess", RuntimeLanguage: language, EntrypointKind: "factory", Factory: "new_extension", SDKName: "pig-sdk-py", Package: "provider_object"}
			case "rust":
				owner = subprocess.ExtConfig{Name: "provider-object-rust", Source: filepath.Join(root, "test/extension-conformance/testdata/provider-object-rust"), Enabled: true, Isolation: "strict", RuntimeKind: "subprocess", RuntimeLanguage: language, EntrypointKind: "factory", Factory: "new_extension", SDKName: "pig-sdk", Package: "provider-object-rust"}
			}
			reader := subprocess.ExtConfig{Name: "extension-provider-carrier-remote", Source: filepath.Join(root, "test/parity/scenarios/testdata/extension-provider-carrier-remote.mjs"), Enabled: true, Isolation: "strict"}
			loaded, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{owner, reader})
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			if err := loaded[1].Commands["carrier-capture"].Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			replacement := filepath.Join(dir, "carrier-replacement.mjs")
			if err := os.WriteFile(replacement, []byte(`export default function (pi) {
  pi.registerProvider("carrier-provider", { api: "openai-completions", baseUrl: "http://127.0.0.1:9", apiKey: "replacement", models: [] });
}
`), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "carrier-replacement", Source: replacement, Enabled: true, Isolation: "strict"}); err != nil {
				t.Fatal(err)
			}
			retained := filepath.Join(dir, "retained.json")
			if err := loaded[1].Commands["carrier-retained"].Handler(t.Context(), retained); err != nil {
				t.Fatalf("the captured Provider failed after its owner was superseded: %v", err)
			}
			body, err := os.ReadFile(retained)
			if err != nil {
				t.Fatal(err)
			}
			// The property is that the capture still reaches its owner's streamSimple; the content's key order is the owner SDK's serialization.
			var content []map[string]string
			if err := json.Unmarshal(body, &content); err != nil || len(content) != 1 || content[0]["type"] != "text" || content[0]["text"] != "simple" {
				t.Fatalf("captured Provider result after supersession: %s", body)
			}
		})
	}
}
