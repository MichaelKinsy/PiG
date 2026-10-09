//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBedrockUpstreamCredentials(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/bedrock-credentials.test.ts:72
	t.Run("prefers explicit and scoped profiles over ambient AWS access keys", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			opts StreamOptions
		}{
			{"explicit-profile", StreamOptions{Profile: "explicit-profile"}},
			{"scoped-profile", StreamOptions{Env: ProviderEnv{"AWS_PROFILE": "scoped-profile"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				isolateBedrockConfig(t)
				t.Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "secretexample")
				cfg, err := loadBedrockConfig(t.Context(), "https://bedrock-runtime.us-east-1.amazonaws.com", "us.anthropic.claude-opus-4-8", tc.opts)
				if err != nil {
					t.Fatal(err)
				}
				got, err := cfg.Credentials.Retrieve(t.Context())
				if err != nil || got.AccessKeyID != tc.name || got.SecretAccessKey != "profile-secret" {
					t.Fatalf("selected credentials = %q, %v; want profile %q", got.AccessKeyID, err, tc.name)
				}
			})
		}
	})
	for _, tc := range []struct{ name, profile string }{
		// .upstream/v0.87.1/packages/ai/test/bedrock-credentials.test.ts:88
		{"uses ambient AWS access keys when no profile is configured", ""},
		// .upstream/v0.87.1/packages/ai/test/bedrock-credentials.test.ts:102
		{"uses ambient AWS access keys when only an ambient profile is set", "ambient-profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateBedrockConfig(t)
			t.Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "secretexample")
			t.Setenv("AWS_PROFILE", tc.profile)
			cfg, err := loadBedrockConfig(t.Context(), "https://bedrock-runtime.us-east-1.amazonaws.com", "us.anthropic.claude-opus-4-8", StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := cfg.Credentials.Retrieve(t.Context())
			if err != nil || got.AccessKeyID != "AKIAEXAMPLE" || got.SecretAccessKey != "secretexample" || got.SessionToken != "" {
				t.Fatalf("credentials = %+v, %v", got, err)
			}
		})
	}
}

func TestBedrockUpstreamEndpointResolution(t *testing.T) {
	const eu = "https://bedrock-runtime.eu-central-1.amazonaws.com"
	const us = "https://bedrock-runtime.us-east-1.amazonaws.com"
	// .upstream/v0.87.1/packages/ai/test/bedrock-endpoint-resolution.test.ts:97
	t.Run("assigns eu-central-1 runtime URLs to built-in EU inference profiles", func(t *testing.T) {
		model := cloneGeneratedModel(t, "amazon-bedrock/eu.anthropic.claude-sonnet-4-5-20250929-v1:0").ToModel()
		if model.ProviderMeta.BaseURL != eu {
			t.Fatalf("baseUrl = %q", model.ProviderMeta.BaseURL)
		}
	})
	for _, tc := range []struct {
		name, base, model, ambientRegion, ambientProfile string
		opts                                             StreamOptions
		region, endpoint                                 string
	}{
		// .upstream/v0.87.1/packages/ai/test/bedrock-endpoint-resolution.test.ts:103
		{"does not pin standard AWS endpoints when AWS_REGION is configured", us, "us.anthropic.claude-opus-4-8", "us-east-2", "", StreamOptions{}, "us-east-2", ""},
		// .upstream/v0.87.1/packages/ai/test/bedrock-endpoint-resolution.test.ts:113
		{"derives region from a built-in EU endpoint when no region or profile is configured", eu, "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", "", "", StreamOptions{}, "eu-central-1", eu},
		// .upstream/v0.87.1/packages/ai/test/bedrock-endpoint-resolution.test.ts:122 (all three captures)
		{"handles missing regions for explicit profiles", eu, "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", "", "", StreamOptions{Profile: "bedrock-profile"}, "eu-central-1", eu},
		{"handles missing regions for scoped profiles", eu, "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", "", "", StreamOptions{Env: ProviderEnv{"AWS_PROFILE": "scoped-bedrock-profile"}}, "eu-central-1", eu},
		{"handles missing regions for ambient profiles", eu, "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", "", "ambient-bedrock-profile", StreamOptions{}, "", ""},
		// .upstream/v0.87.1/packages/ai/test/bedrock-endpoint-resolution.test.ts:145
		{"still passes custom Bedrock endpoints through to the SDK client", "https://bedrock-vpc.example.com", "us.anthropic.claude-opus-4-8", "us-west-2", "", StreamOptions{}, "us-west-2", "https://bedrock-vpc.example.com"},
		// .upstream/v0.87.1/packages/ai/test/bedrock-endpoint-resolution.test.ts:159
		{"extracts region from inference profile ARN regardless of AWS_REGION", us, "arn:aws:bedrock:us-west-2:123456789012:application-inference-profile/abc123", "us-east-1", "", StreamOptions{}, "us-west-2", ""},
		// .upstream/v0.87.1/packages/ai/test/bedrock-endpoint-resolution.test.ts:172
		{"extracts region from GovCloud inference profile ARN", us, "arn:aws-us-gov:bedrock:us-gov-west-1:123456789012:application-inference-profile/abc123", "us-east-1", "", StreamOptions{}, "us-gov-west-1", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateBedrockConfig(t)
			t.Setenv("AWS_REGION", tc.ambientRegion)
			t.Setenv("AWS_PROFILE", tc.ambientProfile)
			cfg, err := loadBedrockConfig(t.Context(), tc.base, tc.model, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			endpoint := ""
			if cfg.BaseEndpoint != nil {
				endpoint = *cfg.BaseEndpoint
			}
			if cfg.Region != tc.region || endpoint != tc.endpoint {
				t.Fatalf("region=%q endpoint=%q; want %q %q", cfg.Region, endpoint, tc.region, tc.endpoint)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/bedrock-endpoint-resolution.test.ts:185
	t.Run("preserves ambient AWS auth for custom model IDs through compat dispatch", func(t *testing.T) {
		isolateBedrockConfig(t)
		t.Setenv("AWS_PROFILE", "bedrock-profile")
		cfg, err := loadBedrockConfig(t.Context(), us, "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/example", StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BearerAuthTokenProvider != nil {
			t.Fatal("ambient profile selected bearer auth")
		}
		got, err := cfg.Credentials.Retrieve(t.Context())
		if err != nil || got.AccessKeyID != "bedrock-profile" {
			t.Fatalf("profile credentials=%q, %v", got.AccessKeyID, err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/bedrock-endpoint-resolution.test.ts:200
	t.Run("uses the generic API key option as a Bedrock bearer token", func(t *testing.T) {
		isolateBedrockConfig(t)
		cfg, err := loadBedrockConfig(t.Context(), us, "us.anthropic.claude-opus-4-8", StreamOptions{APIKey: "bedrock-api-key"})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BearerAuthTokenProvider == nil {
			t.Fatal("missing bearer token provider")
		}
		token, err := cfg.BearerAuthTokenProvider.RetrieveBearerToken(t.Context())
		if err != nil || token.Value != "bedrock-api-key" {
			t.Fatalf("token=%+v, %v", token, err)
		}
		if len(cfg.AuthSchemePreference) != 1 || cfg.AuthSchemePreference[0] != "httpBearerAuth" {
			t.Fatalf("authSchemePreference = %v", cfg.AuthSchemePreference)
		}
		// The caller chooses a custom proxy endpoint, as allowed by Pi's Bedrock client.
		headers := make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			headers <- r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		}))
		defer server.Close()
		provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", server.URL)
		stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{APIKey: "bedrock-api-key"})
		if err != nil {
			t.Fatal(err)
		}
		stream.Result()
		select {
		case got := <-headers:
			if got != "Bearer bedrock-api-key" {
				t.Fatalf("Authorization = %q", got)
			}
		default:
			t.Fatal("stream settled without sending the request")
		}
	})
}
