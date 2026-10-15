package ai

import (
	"encoding/json"
	"testing"
)

// utils/hash.ts shortHash. The expected strings are the output of the pinned TypeScript function (Math.imul over UTF-16 code units, base 36), computed with Node.
func TestShortHashMatchesPiTypeScript(t *testing.T) {
	for input, want := range map[string]string{
		"":                  "k4n83c7h0j2b",
		"abc":               "y0biex7f9bbh",
		"call_123|item_456": "9b5zes1cwdux1",
		"héllo😀":            "1olj6nu1frhbfw",
	} {
		if got := ShortHash(input); got != want {
			t.Errorf("ShortHash(%q) = %q, want %q", input, got, want)
		}
	}
}

// utils/estimate.ts:37-39 estimateTextTokens: ceil(UTF-16 length / 3.5).
func TestEstimateTextTokensIsCeilOfUTF16LengthOverThreePointFive(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int
	}{{"", 0}, {"abc", 1}, {"abcdefg", 2}, {"abcdefgh", 3}, {"😀", 1}, {"😀😀😀😀", 3}} {
		if got := EstimateTextTokens(tc.text); got != tc.want {
			t.Errorf("EstimateTextTokens(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}

// utils/provider-env.ts getProviderEnvValue: `env?.[name] || process.env[name]`: a scoped override wins, an empty override falls through to the process environment, and neither yields the empty string.
func TestGetProviderEnvValueScopedOverrideThenProcessEnvironment(t *testing.T) {
	t.Setenv("PIG_TEST_PROVIDER_ENV_VALUE", "from-process")
	if got := getProviderEnvValue("PIG_TEST_PROVIDER_ENV_VALUE", ProviderEnv{"PIG_TEST_PROVIDER_ENV_VALUE": "scoped"}); got != "scoped" {
		t.Errorf("scoped override = %q, want scoped", got)
	}
	if got := getProviderEnvValue("PIG_TEST_PROVIDER_ENV_VALUE", ProviderEnv{"PIG_TEST_PROVIDER_ENV_VALUE": ""}); got != "from-process" {
		t.Errorf("empty override = %q, want the process value", got)
	}
	if got := getProviderEnvValue("PIG_TEST_PROVIDER_ENV_VALUE", nil); got != "from-process" {
		t.Errorf("no override = %q, want the process value", got)
	}
	if got := getProviderEnvValue("PIG_TEST_PROVIDER_ENV_VALUE_UNSET", nil); got != "" {
		t.Errorf("unset variable = %q, want empty", got)
	}
}

// radius-config.ts:57-59 getRadiusCredentialConfig: the sanitized gatewayConfig of an OAuth credential, undefined when there is none or it is malformed.
func TestGetRadiusCredentialConfigSanitizesTheCredentialsGatewayConfig(t *testing.T) {
	valid := &Credential{Type: CredentialOAuth, GatewayConfig: json.RawMessage(`{"baseUrl":"https://radius.example.com/v1","models":[]}`)}
	config, ok := GetRadiusCredentialConfig(valid)
	if !ok || config.BaseURL != "https://radius.example.com/v1" || len(config.Models) != 0 {
		t.Errorf("valid config = %+v, %v", config, ok)
	}
	for name, credential := range map[string]*Credential{
		"nil credential":      nil,
		"no gateway config":   {Type: CredentialOAuth},
		"malformed config":    {Type: CredentialOAuth, GatewayConfig: json.RawMessage(`{"models":"nope"}`)},
		"config without base": {Type: CredentialOAuth, GatewayConfig: json.RawMessage(`{"models":[]}`)},
	} {
		if config, ok := GetRadiusCredentialConfig(credential); ok {
			t.Errorf("%s produced %+v", name, config)
		}
	}
}
