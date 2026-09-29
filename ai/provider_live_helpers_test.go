//go:build live

package ai

import (
	"os"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func liveProviderKey(t *testing.T, provider string) string {
	t.Helper()
	t.Logf("live provider: %s", provider)
	// upstream: packages/ai/test/oauth.ts:56. Codex has no provider API-key environment variable; tests accept an explicitly supplied resolved OAuth access token.
	if provider == "openai-codex" {
		return testenv.RequireLiveEnv(t, "PIG_LIVE_CODEX_TOKEN")
	}
	names := getAPIKeyEnvVars(provider)
	// upstream: packages/ai/src/env-api-keys.ts:getEnvApiKey excludes the header-only token from API-key resolution.
	names = slices.DeleteFunc(names, func(name string) bool { return name == AnthropicAuthTokenEnv })
	if len(names) == 0 {
		t.Fatalf("no API-key environment variables for live provider %s", provider)
	}
	for _, name := range names {
		if os.Getenv(name) != "" {
			return testenv.RequireLiveEnv(t, name)
		}
	}
	return testenv.RequireLiveEnv(t, names...)
}

// upstream: packages/ai/src/env-api-keys.ts:getEnvApiKey accepts AWS profiles, IAM keys, bearer tokens, ECS task roles, and web identity.
func requireBedrockLiveCredentials(t *testing.T) {
	t.Helper()
	t.Log("live provider: amazon-bedrock")
	if GetEnvAPIKey("amazon-bedrock", nil) != "" {
		return
	}
	t.Log("Bedrock requires AWS_PROFILE, AWS_ACCESS_KEY_ID plus AWS_SECRET_ACCESS_KEY, AWS_BEARER_TOKEN_BEDROCK, an ECS credentials URI, or AWS_WEB_IDENTITY_TOKEN_FILE")
	testenv.RequireLiveEnv(t, "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE")
}
