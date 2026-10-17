//go:build !pig_strip_node_extensions

package subprocess_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The same runtime must cross source loading and Runner.BindCore, not merely reproduce queue results in a test-only registry. None of these registrations makes a provider request or resolves credentials.
func TestHostProviderQueueBindsToRunnerRegistry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		api             ai.API
		oauth, noModels bool
	}{
		{"api-key", ai.APIOpenAICompletions, false, false},
		{"custom-responses", ai.APIOpenAIResponses, false, false},
		{"oauth", ai.APIOpenAICompletions, true, false},
		{"no-default-model", ai.APIOpenAICompletions, false, true},
	} {
		for _, isolation := range []string{"", "isolated"} {
			t.Run(tc.name+"/isolation="+isolation, func(t *testing.T) {
				root := t.TempDir()
				services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: root, AgentDir: filepath.Join(root, "agent")})
				if err != nil {
					t.Fatal(err)
				}
				config := extension.ProviderConfig{API: tc.api, BaseURL: "https://provider.test/v1", APIKey: "provider-test-key", Models: []extension.ProviderModelConfig{{ID: "instant-model", Name: "Instant Model", Input: []string{"text"}, ContextWindow: 128000, MaxTokens: 4096}}}
				if tc.oauth {
					config.APIKey = ""
					config.OAuth = &extension.ProviderOAuth{Name: "Queue OAuth"}
				}
				if tc.noModels {
					config.Models = nil
				}
				data, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				entry := filepath.Join(root, "provider-owner.mjs")
				source := fmt.Sprintf(`export default function(pi) {
  pi.registerProvider("queued-provider", %s);
  pi.registerCommand("remove-provider", {handler: () => pi.unregisterProvider("queued-provider")});
}`, data)
				if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
				host := subprocess.NewHost(root)
				t.Cleanup(func() { host.Shutdown("test done") })
				loaded, failures := host.LoadAll(t.Context(), []subprocess.ExtConfig{{Name: "provider-owner", Source: entry, Enabled: true, Isolation: isolation}})
				if len(failures) != 0 || len(loaded) != 1 {
					t.Fatalf("loaded=%v failures=%v", loaded, failures)
				}
				pending := host.Runtime().PendingProviderRegistrations()
				if len(pending) != 1 || pending[0].Name != "queued-provider" || pending[0].ExtensionPath != entry || pending[0].Config.API != tc.api {
					t.Fatalf("pending=%+v", pending)
				}
				if services.Registry().Find("queued-provider", "instant-model") != nil {
					t.Fatal("provider visible before binding")
				}
				runner := inproc.NewRunner(loaded, root, host.Runtime())
				runner.AddErrorListener(func(err *extension.ExtensionError) { t.Errorf("bind: %+v", err) })
				runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: services.Registry()}, nil)
				if len(host.Runtime().PendingProviderRegistrations()) != 0 {
					t.Fatal("bind did not drain Host queue")
				}
				model := services.Registry().Find("queued-provider", "instant-model")
				if tc.noModels {
					if model != nil {
						t.Fatal("registration invented a default model")
					}
				} else if model == nil || model.ProviderMeta.API != tc.api || model.ProviderMeta.BaseURL != config.BaseURL {
					t.Fatalf("bound model=%+v", model)
				}
				if err := loaded[0].Commands["remove-provider"].Handler(t.Context(), ""); err != nil {
					t.Fatal(err)
				}
				if services.Registry().Find("queued-provider", "instant-model") != nil {
					t.Fatal("remote unregister left a model")
				}
			})
		}
	}
}
