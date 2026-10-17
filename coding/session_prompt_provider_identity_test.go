package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:1673-1697 — prompt authenticates the provider declared by the selected model, not its streaming implementation.
func TestSessionPromptUsesDeclaredProviderIdentity(t *testing.T) {
	for _, configured := range []bool{true, false} {
		name := "declared credentials permit the request"
		if !configured {
			name = "implementation credentials cannot authorize another provider"
		}
		t.Run(name, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true}, fauxReply("done", ai.StopReasonStop, 0))
			configureFauxAuth(t, h.session.services)
			model := *h.session.Model()
			model.ProviderMeta.ProviderID = "declared-provider"
			h.session.Agent().SetModel(&model)
			if configured {
				if err := h.session.services.Auth().Delete(t.Context(), "faux"); err != nil {
					t.Fatal(err)
				}
				if err := h.session.services.Auth().Set("declared-provider", ai.Credential{Type: ai.CredentialAPIKey, Key: "declared-key"}); err != nil {
					t.Fatal(err)
				}
				// Pi's checkAuth finds no auth for a provider it has not registered (models.ts checkAuth), so the declared provider is registered
				// with its key, under a model ID the session does not select.
				if err := h.session.services.Registry().RegisterExtensionProvider("declared-provider", extension.ProviderConfig{API: "openai-completions", APIKey: "declared-key", BaseURL: "https://declared.invalid", Models: []extension.ProviderModelConfig{
					{ID: "declared-auth", Name: "declared-auth", ContextWindow: 128_000, MaxTokens: 4096, Input: []string{"text"}},
				}}); err != nil {
					t.Fatal(err)
				}
			}
			err := h.session.Prompt(t.Context(), "hello")
			if configured {
				if err != nil || h.provider.callCount() != 1 {
					t.Fatalf("configured declared provider: error=%v calls=%d", err, h.provider.callCount())
				}
			} else if err == nil || !strings.Contains(err.Error(), "No API key found for declared-provider.") || h.provider.callCount() != 0 {
				t.Fatalf("unconfigured declared provider: error=%v calls=%d", err, h.provider.callCount())
			}
		})
	}
}
