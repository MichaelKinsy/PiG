package coding

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Red test from the 0.99.1 -> 0.99.2 packages/ai audit (audit-992-ai finding F3).
// providers/anthropic.ts:49-69 (0.99.2) resolves the anthropic provider as configured from the three federation
// variables alone ({auth: {}, env, source: "workload identity federation"}), so Pi's model runtime lists the anthropic
// models as available and selects one by default.
// Probe (isolated HOME, only ANTHROPIC_FEDERATION_RULE_ID, ANTHROPIC_ORGANIZATION_ID, ANTHROPIC_IDENTITY_TOKEN_FILE):
// `pi --list-models claude-sonnet-4-5` lists anthropic/claude-sonnet-4-5 and `pi -p hi` exchanges the identity token
// and answers; pig prints "No models available." and "No API key found for the selected model."
func TestAudit992FederationEnvConfiguresAnthropic(t *testing.T) {
	for _, name := range []string{ai.AnthropicAuthTokenEnv, ai.AnthropicOAuthTokenEnv, ai.AnthropicAPIKeyEnv, ai.AnthropicServiceAccountIDEnv, ai.AnthropicWorkspaceIDEnv} {
		t.Setenv(name, "")
	}
	t.Setenv("HOME", t.TempDir())
	identity := filepath.Join(t.TempDir(), "identity.jwt")
	if err := os.WriteFile(identity, []byte("header.payload.signature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ai.AnthropicFederationRuleIDEnv, "fdrl_test")
	t.Setenv(ai.AnthropicOrganizationIDEnv, "org-test")
	t.Setenv(ai.AnthropicIdentityTokenFileEnv, identity)

	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !services.Registry().ModelRegistry.HasConfiguredAuth("anthropic") {
		t.Error("HasConfiguredAuth(anthropic) = false with workload identity federation configured, want true")
	}
	available, err := services.ModelRuntime().GetAvailable(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(available, func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == "anthropic" }) {
		t.Errorf("no anthropic model is available (%d available models), want the anthropic catalog", len(available))
	}
}
