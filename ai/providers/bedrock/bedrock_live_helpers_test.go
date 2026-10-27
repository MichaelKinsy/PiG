//go:build live

package bedrock

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// upstream: packages/ai/src/env-api-keys.ts:getEnvApiKey accepts AWS profiles, IAM keys, bearer tokens, ECS task roles, and web identity.
func requireBedrockLiveCredentials(t *testing.T) {
	t.Helper()
	t.Log("live provider: amazon-bedrock")
	if ai.GetEnvAPIKey("amazon-bedrock", nil) != "" {
		return
	}
	t.Log("Bedrock requires AWS_PROFILE, AWS_ACCESS_KEY_ID plus AWS_SECRET_ACCESS_KEY, AWS_BEARER_TOKEN_BEDROCK, an ECS credentials URI, or AWS_WEB_IDENTITY_TOKEN_FILE")
	testenv.RequireLiveEnv(t, "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE")
}
