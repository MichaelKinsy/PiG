package subprocess

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Pi's model runtime refuses a registration before it stores anything of it (model-runtime.ts registerProvider), so a registration the registry rejects when the queue drains leaves the
// registration of an earlier extension for the same provider as it was. The host records a loaded extension's registration at once (the replicas, the claim on the provider's callbacks,
// the bridged OAuth login: a reload decides what to keep from them before the queue drains), so the refusal gives them back. Whichever of the two extensions the registry refuses, what
// the other registered stands, and nothing of the refused one is left.
//
// mutation-checked: dropping the withdrawal of the replica record, the release of the claim, or the OAuth undo in queueLoadedProvider's outcome fails the matching assertion.
func TestRejectedLoadRegistrationLeavesAnEarlierRegistrationIntact(t *testing.T) {
	const provider = "shared-prov"
	good := func(name string) *sdk.Extension {
		ext := sdk.New(name)
		ext.RegisterProvider(provider, sdk.ProviderConfig{"api": "openai-completions", "baseUrl": "https://" + name + ".invalid/v1", "apiKey": "key", "models": []any{map[string]any{"id": "m", "name": "M"}}})
		return ext
	}
	bad := func(name string) *sdk.Extension {
		ext := sdk.New(name)
		ext.RegisterProvider(provider, sdk.ProviderConfig{"baseUrl": "https://" + name + ".invalid/v1", "headers": map[string]any{"x-from": name}, "oauth": &sdk.OAuthProvider{
			Name: name + " login",
			Login: func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				return sdk.OAuthCredentials{}, errors.New("unused")
			},
			RefreshToken: func(creds sdk.OAuthCredentials) (sdk.OAuthCredentials, error) { return creds, nil },
			GetAPIKey:    func(creds sdk.OAuthCredentials) string { return creds.Access },
		}})
		return ext
	}
	cases := []struct {
		name          string
		first, second func(string) *sdk.Extension
		survivor      string
	}{
		{"later registration refused", good, bad, "first"},
		{"earlier registration refused", bad, good, "second"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { ai.UnregisterOAuthProvider(provider) })
			var mu sync.Mutex
			registry := map[string]string{}
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			host.SetUIBridge(NewUIBridge(func() {}))
			// The registry refuses a registration with no api, as Pi's validates each registration on its own.
			host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
				if config.API == "" {
					return fmt.Errorf("Provider %s: no \"api\" specified", name)
				}
				mu.Lock()
				registry[name] = config.BaseURL
				mu.Unlock()
				return nil
			}, func(string) {})
			for _, load := range []struct {
				name string
				ext  *sdk.Extension
			}{{"first", tc.first("first")}, {"second", tc.second("second")}} {
				if _, err := host.LoadInProcess(t.Context(), ExtConfig{Name: load.name, Path: "/ext/" + load.name + ".go", Enabled: true}, func(conn net.Conn) error { return load.ext.RunWithConn(conn) }); err != nil {
					t.Fatalf("load %s: %v", load.name, err)
				}
			}
			// Loading asked the registry for nothing.
			if len(registry) != 0 {
				t.Fatalf("the registry was asked while the extensions loaded: %v", registry)
			}
			failures := applyQueuedRegistrations(host)
			refused := "first"
			if tc.survivor == "first" {
				refused = "second"
			}
			if len(failures) != 1 || failures[0].ExtensionPath != "/ext/"+refused+".go" {
				t.Fatalf("diagnostics = %+v, want one for the %s extension", failures, refused)
			}
			if got := registry[provider]; got != "https://"+tc.survivor+".invalid/v1" {
				t.Fatalf("registry holds %q for %s, want the %s extension's registration", got, provider, tc.survivor)
			}
			// The claim on the provider's callbacks is the surviving extension's alone.
			if got := host.ProviderNames(tc.survivor); !slices.Equal(got, []string{provider}) {
				t.Fatalf("%s extension owns %v, want [%s]", tc.survivor, got, provider)
			}
			if got := host.ProviderNames(refused); len(got) != 0 {
				t.Fatalf("the refused %s extension still owns %v", refused, got)
			}
			if got := host.OAuthProviderNames(refused); len(got) != 0 {
				t.Fatalf("the refused %s extension still owns the OAuth login %v", refused, got)
			}
			// No replica of the refused registration is left: the replicas hold the surviving registration alone.
			state := host.uiBridge.ModelRegistryState()
			registered, _ := state["registered"].([]map[string]any)
			if len(registered) != 1 || registered[0]["name"] != provider {
				t.Fatalf("replica providers = %v, want [%s]", registered, provider)
			}
			config, _ := registered[0]["config"].(map[string]json.RawMessage)
			if string(config["baseUrl"]) != `"https://`+tc.survivor+`.invalid/v1"` {
				t.Fatalf("replica baseUrl = %s, want the %s extension's", config["baseUrl"], tc.survivor)
			}
			if _, leaked := config["headers"]; leaked {
				t.Fatalf("the refused registration's headers stayed in the replica: %s", config["headers"])
			}
			if _, ok := ai.GetOAuthProvider(provider); ok {
				t.Fatal("the refused registration's OAuth login stayed registered")
			}
		})
	}
}
