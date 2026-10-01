package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// useFederationEnv isolates the process env to the given workload identity federation variables only.
func useFederationEnv(t *testing.T, federation map[string]string) {
	t.Helper()
	clearAllAuthEnv(t)
	for _, name := range []string{ai.AnthropicFederationRuleIDEnv, ai.AnthropicOrganizationIDEnv, ai.AnthropicIdentityTokenFileEnv, ai.AnthropicServiceAccountIDEnv, ai.AnthropicWorkspaceIDEnv} {
		t.Setenv(name, federation[name])
	}
	t.Setenv("HOME", t.TempDir())
}

func completeFederationEnv() map[string]string {
	return map[string]string{
		ai.AnthropicFederationRuleIDEnv:  "fdrl_test",
		ai.AnthropicOrganizationIDEnv:    "org-test",
		ai.AnthropicIdentityTokenFileEnv: "/identity.jwt",
	}
}

// providers/anthropic.ts:47-69 (0.99.2): the three federation variables alone configure the anthropic provider; any one missing does not.
func TestHasEnvAuthAnthropicFederation(t *testing.T) {
	useFederationEnv(t, completeFederationEnv())
	if !hasEnvAuth("anthropic") {
		t.Error("hasEnvAuth(anthropic) = false with the federation variables set")
	}
	if hasEnvAuth("openai") {
		t.Error("hasEnvAuth(openai) = true: federation configures the anthropic provider only")
	}
	if got := resolveAPIKeyFromEnv("anthropic"); got != "" {
		t.Errorf("resolveAPIKeyFromEnv(anthropic) = %q: federation resolves no API key", got)
	}
	for missing := range completeFederationEnv() {
		partial := completeFederationEnv()
		delete(partial, missing)
		useFederationEnv(t, partial)
		if hasEnvAuth("anthropic") {
			t.Errorf("hasEnvAuth(anthropic) = true without %s", missing)
		}
	}
}

// The picker's authenticated set, with and without an agent dir, lists anthropic for federation-only env.
func TestAuthenticatedProvidersAnthropicFederation(t *testing.T) {
	useFederationEnv(t, completeFederationEnv())
	if !AuthenticatedProviders(t.TempDir())["anthropic"] {
		t.Error("AuthenticatedProviders(agentDir) omits anthropic")
	}
	if !AuthenticatedProviders("")["anthropic"] {
		t.Error("AuthenticatedProviders(\"\") omits anthropic")
	}
	useFederationEnv(t, nil)
	if AuthenticatedProviders(t.TempDir())["anthropic"] {
		t.Error("AuthenticatedProviders lists anthropic without any credential")
	}
}

// model-runtime.ts:638-648: ambient auth reports { configured: true, source: "environment", label: check.source } and the
// anthropic resolver's source for federation is "workload identity federation" (providers/anthropic.ts:68).
func TestProviderAuthStatusAnthropicFederation(t *testing.T) {
	useFederationEnv(t, completeFederationEnv())
	want := ai.AuthStatus{Configured: true, Source: ai.AuthSourceEnvironment, Label: "workload identity federation"}
	registry := NewModelRegistry(t.TempDir())
	if got := registry.GetProviderAuthStatus("anthropic"); got != want {
		t.Errorf("GetProviderAuthStatus = %#v, want %#v", got, want)
	}
	if got := registry.ExtensionProviderAuthStatus("anthropic"); got != want {
		t.Errorf("ExtensionProviderAuthStatus = %#v, want %#v", got, want)
	}
	if !registry.HasConfiguredAuth("anthropic") {
		t.Error("HasConfiguredAuth(anthropic) = false")
	}
	if registry.AvailableProviderCount() != 1 {
		t.Errorf("AvailableProviderCount = %d, want 1", registry.AvailableProviderCount())
	}
	t.Setenv(ai.AnthropicAPIKeyEnv, "sk-ant-key")
	want.Label = ai.AnthropicAPIKeyEnv
	if got := registry.ExtensionProviderAuthStatus("anthropic"); got != want {
		t.Errorf("with an API key: ExtensionProviderAuthStatus = %#v, want %#v (keys win over federation)", got, want)
	}
}
