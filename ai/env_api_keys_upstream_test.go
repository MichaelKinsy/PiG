package ai

import (
	"reflect"
	"testing"
)

func TestEnvAPIKeysUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		env            map[string]string
		keys           []string
		apiKey         string
	}{
		// .upstream/v0.87.1/packages/ai/test/env-api-keys.test.ts:57
		{name: "does not treat generic GitHub tokens as GitHub Copilot credentials", provider: "github-copilot", env: map[string]string{"GH_TOKEN": "gh-token", "GITHUB_TOKEN": "github-token"}},
		// .upstream/v0.87.1/packages/ai/test/env-api-keys.test.ts:66
		{name: "resolves GitHub Copilot credentials from COPILOT_GITHUB_TOKEN", provider: "github-copilot", env: map[string]string{"COPILOT_GITHUB_TOKEN": "copilot-token", "GH_TOKEN": "gh-token", "GITHUB_TOKEN": "github-token"}, keys: []string{"COPILOT_GITHUB_TOKEN"}, apiKey: "copilot-token"},
		// .upstream/v0.87.1/packages/ai/test/env-api-keys.test.ts:75
		{name: "resolves ZAI China Coding Plan credentials from ZAI_CODING_CN_API_KEY", provider: "zai-coding-cn", env: map[string]string{"ZAI_CODING_CN_API_KEY": "zai-coding-cn-token"}, keys: []string{"ZAI_CODING_CN_API_KEY"}, apiKey: "zai-coding-cn-token"},
		// .upstream/v0.87.1/packages/ai/test/env-api-keys.test.ts:82
		{name: "reports ANTHROPIC_AUTH_TOKEN but preserves OAuth token API key lookup", provider: "anthropic", env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token", "ANTHROPIC_OAUTH_TOKEN": "oauth-token", "ANTHROPIC_API_KEY": "api-key"}, keys: []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}, apiKey: "oauth-token"},
		// .upstream/v0.87.1/packages/ai/test/env-api-keys.test.ts:91
		{name: "does not return ANTHROPIC_AUTH_TOKEN as an API key", provider: "anthropic", env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token"}, keys: []string{"ANTHROPIC_AUTH_TOKEN"}},
		// .upstream/v0.87.1/packages/ai/test/env-api-keys.test.ts:100
		{name: "preserves ANTHROPIC_OAUTH_TOKEN as an API key", provider: "anthropic", env: map[string]string{"ANTHROPIC_OAUTH_TOKEN": "oauth-token"}, keys: []string{"ANTHROPIC_OAUTH_TOKEN"}, apiKey: "oauth-token"},
		// .upstream/v0.87.1/packages/ai/test/env-api-keys.test.ts:109
		{name: "falls back to ANTHROPIC_API_KEY for API key lookup", provider: "anthropic", env: map[string]string{"ANTHROPIC_API_KEY": "api-key"}, keys: []string{"ANTHROPIC_API_KEY"}, apiKey: "api-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "ZAI_CODING_CN_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
				t.Setenv(key, tc.env[key])
			}
			if got := FindEnvKeys(tc.provider, nil); !reflect.DeepEqual(got, tc.keys) {
				t.Errorf("FindEnvKeys = %#v, want %#v", got, tc.keys)
			}
			if got := GetEnvAPIKey(tc.provider, nil); got != tc.apiKey {
				t.Errorf("GetEnvAPIKey = %q, want %q", got, tc.apiKey)
			}
		})
	}
}
