package coding

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi #8810: a provider that an extension registers with registerProvider(name, config) and that has a stored credential is
// intermittently not "configured" when startup resolves defaultProvider/defaultModel, so the session starts on another provider's default.
// Startup order here: the extension's registration, the awaited local refresh (agent-session-services.ts), then findInitialModel.
func TestExtensionProviderIsConfiguredWhenTheDefaultModelIsResolved(t *testing.T) {
	for _, order := range []string{"register then refresh", "register only", "refresh in flight while registering"} {
		t.Run(order, func(t *testing.T) { runDefaultProviderRace(t, order) })
	}
}

func runDefaultProviderRace(t *testing.T, order string) {
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			agentDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProvider":"acme","defaultModel":"acme-model"}`), 0o644); err != nil {
				t.Error(err)
				return
			}
			// The credential is stored before startup, so the startup availability pass lists it in storedProviders and registration marks the provider configured (model-runtime.ts markProvisionallyConfigured).
			if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{"acme":{"type":"api_key","key":"demo"}}`), 0o600); err != nil {
				t.Error(err)
				return
			}
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Error(err)
				return
			}
			defer services.Close()
			inFlight := make(chan struct{})
			if order == "refresh in flight while registering" {
				go func() {
					defer close(inFlight)
					_, _ = services.ModelRuntime().GetAvailable(t.Context())
				}()
			} else {
				close(inFlight)
			}
			if err := services.Registry().RegisterExtensionProvider("acme", extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "http://127.0.0.1:9/v1", Models: []extension.ProviderModelConfig{{ID: "acme-model", Name: "Acme Model", ContextWindow: 128000, MaxTokens: 8192}}}); err != nil {
				t.Error(err)
				return
			}
			switch order {
			case "register then refresh":
				if result := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); len(result.Errors) > 0 {
					t.Error(result.Errors)
					return
				}
			case "refresh in flight while registering":
				<-inFlight
			}
			manager, err := SessionManagerFor(services, SessionStartOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			model, fallback := resolveSDKModel(services, manager, nil)
			if model == nil || model.ID != "acme-model" {
				t.Errorf("run %d: started on %v (%q), want acme/acme-model", i, model, fallback)
			}
		})
	}
	wg.Wait()
}
