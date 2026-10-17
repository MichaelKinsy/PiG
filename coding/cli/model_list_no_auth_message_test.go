package cli

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// clearAuthEnvForNoAuthTest clears every provider credential env var that
// AuthenticatedProviders inspects, so a developer's shell keys cannot make the
// no-auth assertion non-deterministic. Kept local to this file so the
// no-auth-message change stays independent of the catalog-filter change.
func clearAuthEnvForNoAuthTest(t *testing.T) {
	t.Helper()
	for _, provider := range ai.ListRuntimeProviders() {
		for _, key := range ai.FindEnvKeys(provider, nil) {
			t.Setenv(key, "")
		}
	}
	for _, key := range []string{
		"OLLAMA_HOST",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_PROFILE", "AWS_SESSION_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION",
		ai.AnthropicFederationRuleIDEnv, ai.AnthropicOrganizationIDEnv, ai.AnthropicIdentityTokenFileEnv,
		ai.AnthropicServiceAccountIDEnv, ai.AnthropicWorkspaceIDEnv,
	} {
		t.Setenv(key, "")
	}
}

// TestPrintModelList_NoAuth_PrintsLoginGuidance guards the --list-models
// no-auth message. Upstream list-models.ts prints
// formatNoModelsAvailableMessage() from auth-guidance.ts; pig must print the
// same guidance rather than an ad-hoc string.
func TestPrintModelList_NoAuth_PrintsLoginGuidance(t *testing.T) {
	clearAuthEnvForNoAuthTest(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	reg := codingagent.NewModelRegistry(dir)

	out := captureStdout(func() { printModelList(reg, dir, "") })

	if !strings.Contains(out, "No models available. Use /login to log into a provider via OAuth or API key.") {
		t.Fatalf("printModelList no-auth output = %q, want upstream login guidance", out)
	}
}
