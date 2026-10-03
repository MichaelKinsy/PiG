package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func captureRegistryWarnings(t *testing.T, run func()) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "warnings")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = file
	defer func() { os.Stderr = previous; _ = file.Close() }()
	run()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func registryInput(baseURL, api string, ids ...string) ProviderConfigInput {
	input := ProviderConfigInput{BaseURL: baseURL, API: ai.API(api), APIKey: "test-key"}
	for _, id := range ids {
		input.Models = append(input.Models, &ai.Model{ID: id, DisplayName: id, Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 100000, MaxOutputTokens: 8000}})
	}
	return input
}
func registerRegistryInput(t *testing.T, s *Services, id string, input ProviderConfigInput) {
	t.Helper()
	if err := s.Registry().RegisterProviderConfig(id, input); err != nil {
		t.Fatal(err)
	}
}
func registryProviderIDs(s *Services, id string) []string {
	var ids []string
	for _, m := range registryModelsForProvider(s.Registry(), id) {
		ids = append(ids, m.ModelID)
	}
	return ids
}
func registryRefresh(t *testing.T, s *Services) {
	t.Helper()
	result := s.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if result.Aborted || len(result.Errors) > 0 {
		t.Fatal(result)
	}
}

func TestModelRegistryDynamicProvidersUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1107
	t.Run("getProviderDisplayName resolves registered, OAuth, built-in, and fallback names", func(t *testing.T) {
		s := registryTestServices(t, "", nil)
		for id, want := range map[string]string{"openai": "OpenAI", "github-copilot": "GitHub Copilot", "zai": "Z.AI", "unknown-provider": "unknown-provider"} {
			if got := s.Registry().GetProviderDisplayName(id); got != want {
				t.Errorf("%s name=%q, want %q", id, got, want)
			}
		}
		input := registryInput("https://provider.test/v1", "openai-completions", "demo-model")
		input.Name = "Named Provider"
		input.Models[0].(*ai.Model).DisplayName = "Demo Model"
		input.Models[0].(*ai.Model).Capabilities.ContextWindow = 128000
		input.Models[0].(*ai.Model).Capabilities.MaxOutputTokens = 4096
		registerRegistryInput(t, s, "named-provider", input)
		if got := s.Registry().GetProviderDisplayName("named-provider"); got != "Named Provider" {
			t.Errorf("registered name=%q", got)
		}
		input.Name = ""
		input.APIKey = ""
		input.OAuth = &ExtensionOAuthConfig{Name: "OAuth Provider", Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
			return ai.Credential{Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Minute).UnixMilli()}, nil
		}, RefreshToken: func(_ context.Context, c ai.Credential) (ai.Credential, error) { return c, nil }, GetAPIKey: func(c ai.Credential) string { return c.Access }}
		registerRegistryInput(t, s, "oauth-provider", input)
		if got := s.Registry().GetProviderDisplayName("oauth-provider"); got != "OAuth Provider" {
			t.Errorf("OAuth name=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1158
	t.Run("modelOverrides apply to dynamically registered provider models", func(t *testing.T) {
		s := registryFromJSON(t, `{"extension-provider":{"modelOverrides":{"extension-model":{"name":"Overridden Extension Model","thinkingLevelMap":{"off":null,"minimal":null,"low":null,"medium":null,"xhigh":"max"},"headers":{"x-model-override":"enabled"}}}}}`)
		input := registryInput("https://provider.test/v1", "openai-completions", "extension-model")
		input.Models[0].(*ai.Model).DisplayName = "Extension Model"
		input.Models[0].(*ai.Model).ProviderMeta.Reasoning = true
		input.Models[0].(*ai.Model).Capabilities.ContextWindow = 128000
		input.Models[0].(*ai.Model).Capabilities.MaxOutputTokens = 4096
		registerRegistryInput(t, s, "extension-provider", input)
		m := mustRegistryModel(t, s, "extension-provider", "extension-model")
		want := ai.ThinkingLevelMap{ai.ThinkingOff: nil, ai.ThinkingMinimal: nil, ai.ThinkingLow: nil, ai.ThinkingMedium: nil, ai.ThinkingXHigh: new("max")}
		if m.DisplayName != "Overridden Extension Model" || !reflect.DeepEqual(m.ThinkingLevelMap, want) || !reflect.DeepEqual(ai.GetSupportedThinkingLevels(m), []ai.ThinkingLevel{ai.ThinkingHigh, ai.ThinkingXHigh}) {
			t.Fatalf("model=%+v levels=%v", m, ai.GetSupportedThinkingLevels(m))
		}
		auth := s.Registry().GetAPIKeyAndHeaders(t.Context(), m)
		if !auth.OK || auth.Headers["x-model-override"] == nil || *auth.Headers["x-model-override"] != "enabled" {
			t.Fatalf("auth=%+v", auth)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1215
	t.Run("stored API key env propagates to request auth and resolves headers", func(t *testing.T) {
		s := registryFromJSON(t, `{"cloudflare-ai-gateway":{"headers":{"x-account":"$CLOUDFLARE_ACCOUNT_ID"}}}`)
		if err := s.Auth().Set("cloudflare-ai-gateway", ai.Credential{Type: ai.CredentialAPIKey, Key: "$CLOUDFLARE_API_KEY", Env: map[string]string{"CLOUDFLARE_API_KEY": "stored-cf-token", "CLOUDFLARE_ACCOUNT_ID": "stored-account", "CLOUDFLARE_GATEWAY_ID": "stored-gateway"}}); err != nil {
			t.Fatal(err)
		}
		entries := registryModelsForProvider(s.Registry(), "cloudflare-ai-gateway")
		if len(entries) == 0 {
			t.Fatal("Cloudflare models missing")
		}
		model := mustRegistryModel(t, s, "cloudflare-ai-gateway", entries[0].ModelID)
		got := s.Registry().GetAPIKeyAndHeaders(t.Context(), model)
		want := ResolvedRequestAuth{OK: true, Headers: ai.ProviderHeaders{"cf-aig-authorization": new("Bearer stored-cf-token"), "Authorization": nil, "x-api-key": nil, "x-account": new("stored-account")}, Env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "stored-account", "CLOUDFLARE_GATEWAY_ID": "stored-gateway"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("auth=%+v, want %+v", got, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1253
	t.Run("registerProvider treats uppercase apiKey and headers as literals", func(t *testing.T) {
		for _, key := range []string{"CUSTOM_NAME", "BEARER", "MODEL_TOKEN"} {
			t.Setenv(key, "env-"+key)
		}
		warnings := captureRegistryWarnings(t, func() {
			s := registryTestServices(t, "", nil)
			input := registryInput("https://provider.test/v1", "openai-completions", "demo-model")
			input.APIKey = "CUSTOM_NAME"
			input.Headers = map[string]string{"Authorization": "BEARER"}
			input.Models[0].(*ai.Model).ProviderMeta.Headers = map[string]string{"x-model-token": "MODEL_TOKEN"}
			registerRegistryInput(t, s, "literal-provider", input)
			if key := s.Registry().GetAPIKeyForProvider(t.Context(), "literal-provider"); key == nil || *key != "CUSTOM_NAME" {
				t.Fatalf("key=%v", key)
			}
			auth := s.Registry().GetAPIKeyAndHeaders(t.Context(), mustRegistryModel(t, s, "literal-provider", "demo-model"))
			want := ResolvedRequestAuth{OK: true, APIKey: new("CUSTOM_NAME"), Headers: ai.ProviderHeaders{"Authorization": new("BEARER"), "x-model-token": new("MODEL_TOKEN")}}
			if !reflect.DeepEqual(auth, want) {
				t.Fatalf("auth=%+v", auth)
			}
		})
		if warnings != "" {
			t.Fatalf("unexpected warnings: %q", warnings)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1306
	t.Run("failed registerProvider does not persist invalid streamSimple config", func(t *testing.T) {
		s := registryTestServices(t, "", nil)
		err := s.Registry().RegisterProviderConfig("broken-provider", ProviderConfigInput{StreamSimple: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return nil, errors.New("should not run")
		}})
		if err == nil || !strings.Contains(err.Error(), `Provider broken-provider: "api" is required when registering streamSimple.`) {
			t.Fatalf("error=%v", err)
		}
		registryRefresh(t, s)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1320
	t.Run("failed registerProvider does not remove existing provider models", func(t *testing.T) {
		s := registryTestServices(t, "", nil)
		input := registryInput("https://provider.test/v1", "openai-completions", "demo-model")
		input.Models[0].(*ai.Model).DisplayName = "Demo Model"
		input.Models[0].(*ai.Model).Capabilities.ContextWindow = 128000
		input.Models[0].(*ai.Model).Capabilities.MaxOutputTokens = 4096
		registerRegistryInput(t, s, "demo-provider", input)
		mustRegistryModel(t, s, "demo-provider", "demo-model")
		input.BaseURL = "https://provider.test/v2"
		input.API = ""
		input.Models = []ai.AnyModel{new(*input.Models[0].(*ai.Model))}
		input.Models[0].(*ai.Model).ID = "broken-model"
		input.Models[0].(*ai.Model).DisplayName = "Broken Model"
		err := s.Registry().RegisterProviderConfig("demo-provider", input)
		if err == nil || !strings.Contains(err.Error(), `Provider demo-provider, model broken-model: no "api" specified.`) {
			t.Fatalf("error=%v", err)
		}
		mustRegistryModel(t, s, "demo-provider", "demo-model")
		registryRefresh(t, s)
		mustRegistryModel(t, s, "demo-provider", "demo-model")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1365
	t.Run("unregisterProvider removes the runtime OAuth overlay without mutating global state", func(t *testing.T) {
		s := registryTestServices(t, "", nil)
		registerRegistryInput(t, s, "anthropic", ProviderConfigInput{OAuth: &ExtensionOAuthConfig{Name: "Custom Anthropic OAuth", Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
			return ai.Credential{Access: "custom-access-token", Refresh: "custom-refresh-token", Expires: time.Now().Add(time.Minute).UnixMilli()}, nil
		}, RefreshToken: func(_ context.Context, c ai.Credential) (ai.Credential, error) { return c, nil }, GetAPIKey: func(c ai.Credential) string { return c.Access }}})
		got := s.Registry().GetRegisteredProviderConfig("anthropic")
		if got == nil || got.OAuth == nil || got.OAuth.Name != "Custom Anthropic OAuth" {
			t.Fatalf("config=%+v", got)
		}
		s.Registry().UnregisterProvider("anthropic")
		if s.Registry().GetRegisteredProviderConfig("anthropic") != nil {
			t.Fatal("OAuth registration retained")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1388
	t.Run("streamSimple overlays do not mutate the global compat API registry", func(t *testing.T) {
		s := registryTestServices(t, "", nil)
		called := false
		registerRegistryInput(t, s, "stream-override-provider", ProviderConfigInput{API: ai.APIOpenAICompletions, StreamSimple: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			called = true
			return nil, errors.New("custom streamSimple override")
		}})
		for i := range 2 {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "openai", Model: "test-openai-model", BaseURL: "https://api.openai.com/v1"})
			_, _ = provider.Stream(ctx, ai.NormalizeContext(ai.Context{}), ai.StreamOptions{})
			if called {
				t.Fatal("local override affected global API")
			}
			if i == 0 {
				s.Registry().UnregisterProvider("stream-override-provider")
			}
		}
	})
	for _, tc := range []struct {
		name, provider string
		first, second  ProviderConfigInput
		want           []string
		url            string
		header         bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1419
		{name: "baseUrl-only override keeps built-in provider models after refresh", provider: "anthropic", first: ProviderConfigInput{BaseURL: "https://proxy.test/anthropic"}, url: "https://proxy.test/anthropic"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1430
		{name: "models-only override replaces built-in provider models after refresh", provider: "anthropic", first: registryInput("https://custom.test/anthropic", "anthropic-messages", "custom-claude"), want: []string{"custom-claude"}, url: "https://custom.test/anthropic"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1443
		{name: "models plus baseUrl override replaces built-in provider models after refresh", provider: "anthropic", first: registryInput("https://custom.test/anthropic", "anthropic-messages", "custom-claude"), second: ProviderConfigInput{BaseURL: "https://proxy.test/anthropic"}, want: []string{"custom-claude"}, url: "https://proxy.test/anthropic"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1457
		{name: "models-only custom provider registration survives refresh", provider: "custom-provider", first: registryInput("https://custom.test/v1", "openai-completions", "custom-a", "custom-b"), want: []string{"custom-a", "custom-b"}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1472
		{name: "baseUrl-only override keeps custom provider models after refresh", provider: "custom-provider", first: registryInput("https://custom.test/v1", "openai-completions", "custom-a", "custom-b"), second: ProviderConfigInput{BaseURL: "https://proxy.test/custom"}, want: []string{"custom-a", "custom-b"}, url: "https://proxy.test/custom"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1493
		{name: "headers-only override keeps custom provider models after refresh", provider: "custom-provider", first: registryInput("https://custom.test/v1", "openai-completions", "custom-a", "custom-b"), second: ProviderConfigInput{Headers: map[string]string{"x-proxy": "enabled"}}, want: []string{"custom-a", "custom-b"}, url: "https://custom.test/v1", header: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := registryTestServices(t, "", nil)
			registerRegistryInput(t, s, tc.provider, tc.first)
			if tc.second.BaseURL != "" || tc.second.Headers != nil {
				registerRegistryInput(t, s, tc.provider, tc.second)
			}
			registryRefresh(t, s)
			ids := registryProviderIDs(s, tc.provider)
			if tc.want != nil && !reflect.DeepEqual(ids, tc.want) || tc.want == nil && len(ids) <= 1 {
				t.Fatalf("models=%v, want %v", ids, tc.want)
			}
			if tc.url != "" {
				for _, m := range registryModelsForProvider(s.Registry(), tc.provider) {
					if m.BaseURL != tc.url {
						t.Errorf("URL=%q, want %q", m.BaseURL, tc.url)
					}
				}
			}
			if tc.header {
				auth := s.Registry().GetAPIKeyAndHeaders(t.Context(), mustRegistryModel(t, s, tc.provider, ids[0]))
				if !auth.OK || auth.Headers["x-proxy"] == nil || *auth.Headers["x-proxy"] != "enabled" {
					t.Fatalf("auth=%+v", auth)
				}
			}
		})
	}
}
