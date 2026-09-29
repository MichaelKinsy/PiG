package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
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
			if err := h.session.services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
				t.Fatal(err)
			}
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
			}
			_, err := h.session.Prompt(t.Context(), "hello")
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
