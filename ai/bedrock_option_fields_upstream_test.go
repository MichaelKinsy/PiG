//go:build !pig_strip_bedrock_converse_stream

package ai

import "testing"

// upstream: bedrock-converse-stream.ts:1266 display = options.thinkingDisplay ?? "summarized", omitted on GovCloud.
func TestBedrockThinkingDisplayOption(t *testing.T) {
	isolateBedrockConfig(t)
	model := cloneGeneratedModel(t, "amazon-bedrock/us.anthropic.claude-sonnet-4-5-20250929-v1:0").ToModel()
	fields := func(display AnthropicThinkingDisplay, region string) map[string]any {
		additional := buildBedrockAdditionalFields(model, "", StreamOptions{Thinking: "low", IsReasoning: true, ThinkingDisplay: display, Region: region})
		thinking, _ := additional["thinking"].(map[string]any)
		return thinking
	}
	if got := fields("", "us-east-1")["display"]; got != "summarized" {
		t.Fatalf("default display = %#v, want summarized", got)
	}
	if got := fields(AnthropicThinkingDisplayOmitted, "us-east-1")["display"]; got != "omitted" {
		t.Fatalf("display = %#v, want omitted", got)
	}
	if got, present := fields(AnthropicThinkingDisplayOmitted, "us-gov-west-1")["display"]; present {
		t.Fatalf("GovCloud display = %#v, want the field skipped", got)
	}
}

// upstream: bedrock-converse-stream.ts:184 bearerToken = options.bearerToken || options.apiKey || AWS_BEARER_TOKEN_BEDROCK.
func TestBedrockBearerTokenOptionPrecedence(t *testing.T) {
	isolateBedrockConfig(t)
	env := ProviderEnv{"AWS_BEARER_TOKEN_BEDROCK": "from-env", "AWS_REGION": "us-east-1"}
	for _, tc := range []struct {
		name    string
		options StreamOptions
		want    string
	}{
		{"environment", StreamOptions{Env: env}, "from-env"},
		{"api key beats environment", StreamOptions{Env: env, APIKey: "from-key"}, "from-key"},
		{"bearer token beats api key", StreamOptions{Env: env, APIKey: "from-key", BearerToken: "from-option"}, "from-option"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadBedrockConfig(t.Context(), "", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", tc.options)
			if err != nil {
				t.Fatal(err)
			}
			token, err := cfg.BearerAuthTokenProvider.RetrieveBearerToken(t.Context())
			if err != nil || token.Value != tc.want {
				t.Fatalf("bearer token = %q, %v; want %q", token.Value, err, tc.want)
			}
		})
	}
}
