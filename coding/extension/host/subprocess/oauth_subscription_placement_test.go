package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Each packed member owns its OAuth metadata even when the process is shared.
// The same provider declarations also run isolated, so neither SDK encoding nor
// cell placement can silently replace explicit true with a false default.
func TestOAuthSubscriptionSDKPlacements(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	for _, language := range []string{"go", "python", "rust"} {
		for _, packed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/packed=%t", language, packed), func(t *testing.T) {
				host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
				t.Cleanup(func() { host.Shutdown("test done") })
				configs := []ExtConfig{
					writeSubscriptionFactory(t, root, language, "subscription_yes", true),
					writeSubscriptionFactory(t, root, language, "subscription_no", false),
				}
				if !packed {
					for i := range configs {
						configs[i].Isolation = "isolated"
					}
				}
				loaded, errs := host.LoadAll(t.Context(), configs)
				if len(errs) != 0 || len(loaded) != len(configs) {
					t.Fatalf("load: %v (%d)", errs, len(loaded))
				}
				first, second := host.exts[configs[0].Name], host.exts[configs[1].Name]
				if shared := first.packedCellKey != "" && first.packedCellKey == second.packedCellKey; shared != packed {
					t.Fatalf("shared cell = %t, want %t", shared, packed)
				}
				for _, config := range configs {
					if _, ok := ai.GetOAuthProvider(config.Name); !ok {
						t.Fatalf("provider %s missing", config.Name)
					}
					if got, want := ai.IsOAuthSubscriptionProvider(config.Name), config.Name == "subscription_yes"; got != want {
						t.Errorf("%s subscription=%t, want %t", config.Name, got, want)
					}
				}
				host.Shutdown("removed")
				for _, config := range configs {
					if _, ok := ai.GetOAuthProvider(config.Name); ok {
						t.Errorf("provider %s retained after shutdown", config.Name)
					}
				}
			})
		}
	}
}

func writeSubscriptionFactory(t *testing.T, root, language, name string, subscription bool) ExtConfig {
	t.Helper()
	dir := t.TempDir()
	write := func(path, data string) {
		t.Helper()
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sdkManifest := func(sdk, filename string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "extensions", sdk, filename))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	switch language {
	case "go":
		module := "example.com/" + name
		manifest := strings.Replace(sdkManifest("sdk", "go.mod"), "module github.com/MichaelKinsy/PiG/extensions/sdk", "module "+module, 1)
		write("go.mod", manifest+"\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n")
		write("extension.go", fmt.Sprintf(`package fixture
import "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension() *sdk.Extension {
 e := sdk.New(%q)
 e.RegisterProvider(%q, sdk.ProviderConfig{"oauth": &sdk.OAuthProvider{IsSubscription: %t, Login: func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) { return sdk.OAuthCredentials{}, nil }}})
 return e
}
`, name, name, subscription))
		return packedFactoryConfig(name, dir, module, name)
	case "python":
		value := "False"
		if subscription {
			value = "True"
		}
		write(name+".py", fmt.Sprintf(`import pig_sdk
def new_extension():
    ext = pig_sdk.Extension(%q)
    ext.register_oauth_provider(%q, {}, pig_sdk.OAuthProvider(is_subscription=%s, login=lambda cb: pig_sdk.OAuthCredentials()))
    return ext
`, name, name, value))
		return packedPythonFactoryConfig(name, dir, name, name)
	case "rust":
		manifest := strings.Replace(sdkManifest("sdk-rs", "Cargo.toml"), `name = "pig-sdk"`, fmt.Sprintf("name = %q", name), 1)
		write("Cargo.toml", manifest+fmt.Sprintf("pig-sdk = { path = %q }\n", filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs"))))
		write("src/lib.rs", fmt.Sprintf(`use pig_sdk::{Extension, OAuthProvider, OAuthCredentials};
pub fn new_extension() -> Extension {
 let mut ext = Extension::new(%q);
 ext.register_oauth_provider(%q, serde_json::json!({}), OAuthProvider {
  name: String::new(), is_subscription: %t, login: Box::new(|_| Ok(OAuthCredentials::default())),
  refresh_token: None, get_api_key: None, credential_store: None,
 });
 ext
}
`, name, name, subscription))
		return packedRustFactoryConfig(name, dir, name, name)
	default:
		t.Fatalf("unsupported test language %s", language)
		return ExtConfig{}
	}
}
