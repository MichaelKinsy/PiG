package ai

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func providerModelUpstream(t *testing.T, provider, id string) *GeneratedModel {
	t.Helper()
	model, ok := LookupModelExact(provider + "/" + id)
	if !ok {
		t.Fatalf("missing %s/%s", provider, id)
	}
	return model
}
func providerAuthContextUpstream(env map[string]string, files ...string) AuthContext {
	return AuthContext{Env: func(name string) (string, bool) { value, ok := env[name]; return value, ok }, FileExists: func(path string) bool { return slices.Contains(files, path) }}
}
func resolveProviderAuthUpstream(t *testing.T, id string, env map[string]string, files ...string) *AuthResult {
	t.Helper()
	auth, err := BuiltinProviderAuth(id)
	if err != nil {
		t.Fatal(err)
	}
	result, err := auth.APIKey.Resolve(t.Context(), APIKeyAuthInput{Ctx: providerAuthContextUpstream(env, files...)})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBuiltinProvidersUpstream(t *testing.T) {
	// .upstream/v0.99.1/packages/ai/test/providers.test.ts:56
	t.Run("builtinModels registers every builtin provider with models", func(t *testing.T) {
		providers := ListProviders()
		if len(BuiltinProviders()) != len(providers) {
			t.Fatalf("BuiltinProviders() = %d providers, ListProviders() = %d", len(BuiltinProviders()), len(providers))
		}
		if !slices.Contains(providers, "anthropic") || len(ListModels("")) <= 500 {
			t.Fatal("incomplete builtin catalog")
		}
		if providerModelUpstream(t, "anthropic", "claude-haiku-4-5").API != APIAnthropicMessages {
			t.Fatal("wrong Anthropic API")
		}
		for _, provider := range providers {
			models := GetAllBuiltinModels(provider)
			if len(models) == 0 {
				t.Fatal(provider)
			}
			for _, model := range models {
				if model.ProviderID() != provider {
					t.Fatal(model)
				}
			}
		}
		radius := providerModelUpstream(t, "radius", "balanced")
		if radius.API != APIPiMessages || radius.Provider != "radius" {
			t.Fatal(radius)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:73
	t.Run("stores native constrained-sampling capabilities in model metadata", func(t *testing.T) {
		gpt4 := providerModelUpstream(t, "openai", "gpt-4o")
		gpt5 := providerModelUpstream(t, "openai", "gpt-5.4")
		anth := providerModelUpstream(t, "anthropic", "claude-haiku-4-5")
		if gpt4.Compat == nil || gpt4.Compat.SupportsStrictMode == nil || !*gpt4.Compat.SupportsStrictMode || gpt4.Compat.SupportsOpenAIGrammarTools != nil || gpt5.Compat == nil || gpt5.Compat.SupportsStrictMode == nil || !*gpt5.Compat.SupportsStrictMode || gpt5.Compat.SupportsOpenAIGrammarTools == nil || !*gpt5.Compat.SupportsOpenAIGrammarTools || anth.Compat == nil || anth.Compat.SupportsStrictTools == nil || !*anth.Compat.SupportsStrictTools {
			t.Fatal("incorrect constrained sampling metadata")
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:84
	t.Run("keeps the conservative resize profile on every vision model", func(t *testing.T) {
		count := 0
		want := &ModelImageResizeOptions{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 4718592, JPEGQuality: 80}
		for _, model := range ListModels("") {
			if !slices.Contains(model.Capabilities, "image") {
				continue
			}
			count++
			if model.InputLimits == nil || model.InputLimits.Images == nil || !reflect.DeepEqual(model.InputLimits.Images.Resize, want) {
				t.Fatalf("resize for %s/%s=%#v", model.Provider, model.ID, model.InputLimits)
			}
		}
		if count == 0 {
			t.Fatal("no vision models")
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:94
	t.Run("records known direct-provider image request limits", func(t *testing.T) {
		for _, row := range []struct {
			provider, id                  string
			bytes, perRequest, perMessage int
		}{{"anthropic", "claude-haiku-4-5", 32 * 1024 * 1024, 100, 0}, {"anthropic", "claude-opus-5", 0, 600, 0}, {"amazon-bedrock", "anthropic.claude-haiku-4-5-20251001-v1:0", 0, 0, 20}, {"openai", "gpt-4o", 512 * 1024 * 1024, 1500, 0}, {"google", "gemini-2.5-flash", 20 * 1024 * 1024, 3600, 0}} {
			limits := providerModelUpstream(t, row.provider, row.id).InputLimits
			if limits == nil || limits.Images == nil || (row.bytes != 0 && limits.MaxRequestBytes != row.bytes) || (row.perRequest != 0 && limits.Images.MaxPerRequest != row.perRequest) || (row.perMessage != 0 && limits.Images.MaxPerMessage != row.perMessage) {
				t.Fatalf("limits for %s/%s=%#v", row.provider, row.id, limits)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:113
	t.Run("does not infer image limits from gateway API compatibility", func(t *testing.T) {
		for _, model := range ListModels("openrouter") {
			if slices.Contains(model.Capabilities, "image") {
				want := &ModelInputLimits{Images: &ModelImageInputLimits{Resize: &ModelImageResizeOptions{MaxWidth: 2000, MaxHeight: 2000, MaxBytes: 4718592, JPEGQuality: 80}}}
				if !reflect.DeepEqual(model.InputLimits, want) {
					t.Fatal(model.InputLimits)
				}
				return
			}
		}
		t.Fatal("no OpenRouter vision model")
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:118
	t.Run("uses models.dev effort levels for Google thinking models", func(t *testing.T) {
		for _, provider := range []string{"google", "google-vertex"} {
			if !slices.Contains(GetSupportedThinkingLevels(providerModelUpstream(t, provider, "gemini-3.6-flash").ToModel()), ThinkingMinimal) {
				t.Fatal("minimal missing")
			}
			for _, id := range []string{"gemini-3.8-flash", "gemini-3.1-pro-preview"} {
				if got := GetSupportedThinkingLevels(providerModelUpstream(t, provider, id).ToModel()); !reflect.DeepEqual(got, []ThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh}) {
					t.Fatal(provider, id, got)
				}
			}
		}
		if got := GetSupportedThinkingLevels(providerModelUpstream(t, "opencode", "gemini-3.8-flash").ToModel()); !reflect.DeepEqual(got, []ThinkingLevel{ThinkingLow, ThinkingMedium, ThinkingHigh}) {
			t.Fatal(got)
		}
		if got := GetSupportedThinkingLevels(providerModelUpstream(t, "google", "gemma-4-31b-it").ToModel()); !reflect.DeepEqual(got, []ThinkingLevel{ThinkingMinimal, ThinkingHigh}) {
			t.Fatal(got)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/providers.test.ts:162 (0.99.1 moved the Fireworks unsupported row from kimi-k2p6 to nemotron-3-ultra-nvfp4)
	t.Run("enables mid-conversation system messages only for verified models", func(t *testing.T) {
		supported := [][2]string{{"moonshotai", "kimi-k2.6"}, {"moonshotai", "kimi-k2.7-code"}, {"moonshotai", "kimi-k2.7-code-highspeed"}, {"moonshotai", "kimi-k3"}, {"moonshotai-cn", "kimi-k2.6"}, {"moonshotai-cn", "kimi-k2.7-code"}, {"moonshotai-cn", "kimi-k2.7-code-highspeed"}, {"moonshotai-cn", "kimi-k3"}, {"fireworks", "accounts/fireworks/models/kimi-k3"}, {"fireworks", "accounts/fireworks/routers/kimi-k3-fast"}, {"openai", "gpt-5.4"}, {"openai", "gpt-5.5"}, {"openai", "gpt-6-astra"}, {"openai-codex", "gpt-5.5"}, {"anthropic", "claude-opus-5"}, {"opencode", "gpt-5.4"}, {"opencode", "gpt-5.6-terra"}, {"opencode-go", "gpt-5.6-luna"}, {"opencode", "claude-opus-4-8"}, {"opencode", "claude-opus-5"}, {"opencode", "kimi-k3"}, {"opencode-go", "kimi-k3"}, {"github-copilot", "gpt-5.6-terra"}, {"github-copilot", "claude-opus-5"}, {"github-copilot", "claude-opus-4.8"}, {"github-copilot", "kimi-k3"}, {"deepseek", "deepseek-v4-pro"}, {"openrouter", "openai/gpt-5.6-terra"}}
		unsupported := [][2]string{{"fireworks", "accounts/fireworks/models/nemotron-3-ultra-nvfp4"}, {"openai", "gpt-4.1"}, {"openai", "gpt-5.2"}, {"anthropic", "claude-sonnet-4-5"}, {"google", "gemini-2.5-pro"}, {"opencode", "gpt-5.2"}, {"opencode", "claude-sonnet-4-5"}, {"github-copilot", "claude-sonnet-4.6"}, {"deepseek", "deepseek-flash"}, {"openrouter", "anthropic/claude-opus-5"}, {"openrouter", "moonshotai/kimi-k3"}, {"openrouter", "openai/gpt-5.6-terra:batch"}}
		for _, row := range supported {
			model := providerModelUpstream(t, row[0], row[1])
			if model.Compat == nil || model.Compat.SupportsMidConvoSystemMessages == nil || !*model.Compat.SupportsMidConvoSystemMessages {
				t.Fatal(row)
			}
		}
		for _, row := range unsupported {
			model := providerModelUpstream(t, row[0], row[1])
			if model.Compat != nil && model.Compat.SupportsMidConvoSystemMessages != nil {
				t.Fatal(row)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:200
	t.Run("routes proxied tool changes through verified transports only", func(t *testing.T) {
		for _, provider := range []string{"opencode", "github-copilot"} {
			compat := providerModelUpstream(t, provider, "gpt-5.6-terra").Compat
			if compat == nil || compat.SupportsAdditionalTools == nil || !*compat.SupportsAdditionalTools || compat.SupportsToolSearch != nil {
				t.Fatal(provider, compat)
			}
			if value := providerModelUpstream(t, provider, "claude-opus-5").Compat; value != nil && value.SupportsMidConvoToolChanges != nil {
				t.Fatal(provider)
			}
		}
		if value := providerModelUpstream(t, "anthropic", "claude-opus-5").Compat; value == nil || value.SupportsMidConvoToolChanges == nil || !*value.SupportsMidConvoToolChanges {
			t.Fatal(value)
		}
		for _, provider := range []string{"moonshotai", "moonshotai-cn", "opencode", "opencode-go"} {
			value := providerModelUpstream(t, provider, "kimi-k3").Compat
			if value == nil || value.SupportsMidConvoToolAdditions == nil || !*value.SupportsMidConvoToolAdditions {
				t.Fatal(provider, value)
			}
		}
		for _, provider := range []string{"moonshotai", "moonshotai-cn"} {
			for _, id := range []string{"kimi-k2.6", "kimi-k2.7-code", "kimi-k2.7-code-highspeed"} {
				if value := providerModelUpstream(t, provider, id).Compat; value != nil && value.SupportsMidConvoToolAdditions != nil {
					t.Fatal(provider, id)
				}
			}
		}
		for _, row := range [][2]string{{"github-copilot", "kimi-k3"}, {"openrouter", "openai/gpt-5.6-terra"}} {
			if value := providerModelUpstream(t, row[0], row[1]).Compat; value != nil && value.SupportsMidConvoToolAdditions != nil {
				t.Fatal(row)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:242
	t.Run("uses official Kimi K3 pricing for Moonshot providers", func(t *testing.T) {
		for _, provider := range []string{"moonshotai", "moonshotai-cn"} {
			model := providerModelUpstream(t, provider, "kimi-k3")
			if model.InputCostPerMTokens != 3 || model.OutputCostPerMTokens != 15 || model.CacheReadCost != 0.3 || model.CacheWriteCost != 0 {
				t.Fatal(model)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:254
	t.Run("uses API-equivalent implied pricing for Kimi Coding subscription models", func(t *testing.T) {
		for _, row := range []struct {
			id                  string
			input, output, read float64
		}{{"k3", 3, 15, 0.3}, {"kimi-for-coding-highspeed", 1.9, 8, 0.38}} {
			model := providerModelUpstream(t, "kimi-coding", row.id)
			if model.InputCostPerMTokens != row.input || model.OutputCostPerMTokens != row.output || model.CacheReadCost != row.read || model.CacheWriteCost != 0 {
				t.Fatal(model)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:266
	t.Run("resolves Anthropic bearer auth from env with auth token precedence", func(t *testing.T) {
		result := resolveProviderAuthUpstream(t, "anthropic", map[string]string{"ANTHROPIC_AUTH_TOKEN": "auth-token", "ANTHROPIC_OAUTH_TOKEN": "oauth-token", "ANTHROPIC_API_KEY": "api-key"})
		want := &AuthResult{Auth: ModelAuth{Headers: ProviderHeaders{"Authorization": new("Bearer auth-token")}}, Source: "ANTHROPIC_AUTH_TOKEN"}
		if !reflect.DeepEqual(result, want) {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:282
	t.Run("preserves Anthropic OAuth token precedence over the API key", func(t *testing.T) {
		result := resolveProviderAuthUpstream(t, "anthropic", map[string]string{"ANTHROPIC_API_KEY": "key", "ANTHROPIC_OAUTH_TOKEN": "oauth-token"})
		if result == nil || result.Auth.APIKey != "oauth-token" || result.Source != "ANTHROPIC_OAUTH_TOKEN" {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:328
	t.Run("reports bedrock as configured from ambient AWS credentials without an api key", func(t *testing.T) {
		result := resolveProviderAuthUpstream(t, "amazon-bedrock", map[string]string{"AWS_PROFILE": "dev"})
		if result == nil || !reflect.DeepEqual(result.Auth, ModelAuth{}) || result.Source != "AWS_PROFILE" {
			t.Fatal(result)
		}
		if missing := resolveProviderAuthUpstream(t, "amazon-bedrock", map[string]string{}); missing != nil {
			t.Fatal(missing)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:342
	t.Run("requires Cloudflare Workers AI account config and returns scoped env", func(t *testing.T) {
		if result := resolveProviderAuthUpstream(t, "cloudflare-workers-ai", map[string]string{"CLOUDFLARE_API_KEY": "cf-key"}); result != nil {
			t.Fatal(result)
		}
		result := resolveProviderAuthUpstream(t, "cloudflare-workers-ai", map[string]string{"CLOUDFLARE_API_KEY": "cf-key", "CLOUDFLARE_ACCOUNT_ID": "account-id"})
		if result == nil || result.Auth.APIKey != "cf-key" || !reflect.DeepEqual(result.Env, map[string]string{"CLOUDFLARE_ACCOUNT_ID": "account-id"}) {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:357
	t.Run("requires Cloudflare AI Gateway account and gateway config and returns scoped env headers", func(t *testing.T) {
		if result := resolveProviderAuthUpstream(t, "cloudflare-ai-gateway", map[string]string{"CLOUDFLARE_API_KEY": "cf-key", "CLOUDFLARE_ACCOUNT_ID": "account-id"}); result != nil {
			t.Fatal(result)
		}
		result := resolveProviderAuthUpstream(t, "cloudflare-ai-gateway", map[string]string{"CLOUDFLARE_API_KEY": "cf-key", "CLOUDFLARE_ACCOUNT_ID": "account-id", "CLOUDFLARE_GATEWAY_ID": "gateway-id"})
		want := ModelAuth{Headers: ProviderHeaders{"cf-aig-authorization": new("Bearer cf-key"), "Authorization": nil, "x-api-key": nil}}
		if result == nil || !reflect.DeepEqual(result.Auth, want) || !reflect.DeepEqual(result.Env, map[string]string{"CLOUDFLARE_ACCOUNT_ID": "account-id", "CLOUDFLARE_GATEWAY_ID": "gateway-id"}) {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:431
	t.Run("resolves vertex via ADC file plus project and location", func(t *testing.T) {
		result := resolveProviderAuthUpstream(t, "google-vertex", map[string]string{"GOOGLE_CLOUD_PROJECT": "proj", "GOOGLE_CLOUD_LOCATION": "us-central1"}, vertexADCPath)
		if result == nil || !reflect.DeepEqual(result.Auth, ModelAuth{}) || !strings.Contains(result.Source, "application default") {
			t.Fatal(result)
		}
		if partial := resolveProviderAuthUpstream(t, "google-vertex", map[string]string{"GOOGLE_CLOUD_PROJECT": "proj"}, vertexADCPath); partial != nil {
			t.Fatal(partial)
		}
		if keyed := resolveProviderAuthUpstream(t, "google-vertex", map[string]string{"GOOGLE_CLOUD_API_KEY": "vertex-key"}); keyed == nil || keyed.Auth.APIKey != "vertex-key" {
			t.Fatal(keyed)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:456
	t.Run("prefers the stored credential key and falls back through env vars in order", func(t *testing.T) {
		auth := EnvAPIKeyAuth("Test key", "FIRST_KEY", "SECOND_KEY")
		for _, row := range []struct {
			env         map[string]string
			credential  *Credential
			key, source string
		}{{map[string]string{"FIRST_KEY": "env"}, &Credential{Type: CredentialAPIKey, Key: "stored"}, "stored", "stored credential"}, {map[string]string{"SECOND_KEY": "second"}, nil, "second", "SECOND_KEY"}, {map[string]string{}, nil, "", ""}} {
			result, err := auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: providerAuthContextUpstream(row.env), Credential: row.credential})
			if err != nil {
				t.Fatal(err)
			}
			if row.key == "" {
				if result != nil {
					t.Fatal(result)
				}
			} else if result == nil || result.Auth.APIKey != row.key || result.Source != row.source {
				t.Fatal(result)
			}
		}
	})
	// .upstream/v0.87.1/packages/ai/test/providers.test.ts:734
	t.Run("streams queued responses through a Models collection", func(t *testing.T) {
		p := NewFauxProvider(FauxConfig{})
		p.SetResponses([]FauxResponseStep{fauxUpstreamText("hello from faux")})
		result := fauxUpstreamComplete(t, p.GetModel().Provider, fauxUpstreamRequest(), StreamOptions{})
		if result.StopReason != StopReasonStop || !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: "hello from faux"}}) || p.CallCount() != 1 {
			t.Fatal(result)
		}
	})
}

// getBuiltinProviders is Object.keys(MODELS) (providers/all.ts:94-96), so a provider whose shard holds only classifier
// models (typesafe) is a builtin provider, and the list keeps the barrel's order.
func TestListProvidersMatchesPinnedBarrelKeys(t *testing.T) {
	data, err := os.ReadFile("../.upstream/current/packages/ai/src/models.generated.ts")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(data), "} = {\n")
	if !ok {
		t.Fatal("MODELS initializer not found in the pinned barrel")
	}
	body, _, _ = strings.Cut(body, "\n};")
	var want []string
	for line := range strings.SplitSeq(body, "\n") {
		if id, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(line), `"`), `":`); ok {
			want = append(want, id)
		}
	}
	if len(want) < 40 || !slices.Contains(want, "typesafe") {
		t.Fatalf("barrel keys = %v", want)
	}
	if got := ListProviders(); !slices.Equal(got, want) {
		t.Fatalf("ListProviders() = %v\nwant barrel keys %v", got, want)
	}
	var built []string
	for _, provider := range BuiltinProviders() {
		built = append(built, provider.ID)
	}
	if !slices.Equal(built, want) {
		t.Fatalf("BuiltinProviders() ids = %v\nwant %v", built, want)
	}
}
