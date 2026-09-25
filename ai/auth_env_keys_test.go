package ai

// Ports upstream packages/ai/test/env-api-keys.test.ts and the environment
// case of packages/ai/test/qwen-token-plan-models.test.ts.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// clearEnvKeyVars unsets every variable the table reads, so each case starts
// from an empty environment.
func clearEnvKeyVars(t *testing.T) {
	t.Helper()
	vertexADCCache.Lock()
	previous := vertexADCCache.exists
	vertexADCCache.exists = nil
	vertexADCCache.Unlock()
	t.Cleanup(func() {
		vertexADCCache.Lock()
		vertexADCCache.exists = previous
		vertexADCCache.Unlock()
	})
	names := []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", AnthropicAuthTokenEnv, AnthropicOAuthTokenEnv, AnthropicAPIKeyEnv,
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION",
		"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE"}
	for _, envVar := range envAPIKeyVars {
		names = append(names, envVar)
	}
	for _, name := range names {
		t.Setenv(name, "")
	}
	t.Setenv("HOME", t.TempDir())
}

func TestEnvAPIKeysDoNotTreatGenericGitHubTokensAsCopilotCredentials(t *testing.T) {
	clearEnvKeyVars(t)
	t.Setenv("GH_TOKEN", "gh-token")
	t.Setenv("GITHUB_TOKEN", "github-token")
	if got := FindEnvKeys("github-copilot", nil); got != nil {
		t.Fatalf("FindEnvKeys = %v", got)
	}
	if got := GetEnvAPIKey("github-copilot", nil); got != "" {
		t.Fatalf("GetEnvAPIKey = %q", got)
	}
}

func TestEnvAPIKeysResolveCopilotFromCopilotGitHubToken(t *testing.T) {
	clearEnvKeyVars(t)
	t.Setenv("COPILOT_GITHUB_TOKEN", "copilot-token")
	t.Setenv("GH_TOKEN", "gh-token")
	t.Setenv("GITHUB_TOKEN", "github-token")
	if got := FindEnvKeys("github-copilot", nil); !slices.Equal(got, []string{"COPILOT_GITHUB_TOKEN"}) {
		t.Fatalf("FindEnvKeys = %v", got)
	}
	if got := GetEnvAPIKey("github-copilot", nil); got != "copilot-token" {
		t.Fatalf("GetEnvAPIKey = %q", got)
	}
}

func TestEnvAPIKeysResolveZAIChinaCodingPlan(t *testing.T) {
	clearEnvKeyVars(t)
	t.Setenv("ZAI_CODING_CN_API_KEY", "zai-coding-cn-token")
	if got := FindEnvKeys("zai-coding-cn", nil); !slices.Equal(got, []string{"ZAI_CODING_CN_API_KEY"}) {
		t.Fatalf("FindEnvKeys = %v", got)
	}
	if got := GetEnvAPIKey("zai-coding-cn", nil); got != "zai-coding-cn-token" {
		t.Fatalf("GetEnvAPIKey = %q", got)
	}
}

func TestEnvAPIKeysAnthropicAuthTokenIsReportedButNotAnAPIKey(t *testing.T) {
	clearEnvKeyVars(t)
	t.Setenv(AnthropicAuthTokenEnv, "auth-token")
	t.Setenv(AnthropicOAuthTokenEnv, "oauth-token")
	t.Setenv(AnthropicAPIKeyEnv, "api-key")
	if got := FindEnvKeys("anthropic", nil); !slices.Equal(got, []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}) {
		t.Fatalf("FindEnvKeys = %v", got)
	}
	if got := GetEnvAPIKey("anthropic", nil); got != "oauth-token" {
		t.Fatalf("GetEnvAPIKey = %q", got)
	}

	t.Setenv(AnthropicOAuthTokenEnv, "")
	t.Setenv(AnthropicAPIKeyEnv, "")
	if got := FindEnvKeys("anthropic", nil); !slices.Equal(got, []string{"ANTHROPIC_AUTH_TOKEN"}) {
		t.Fatalf("FindEnvKeys(auth token only) = %v", got)
	}
	if got := GetEnvAPIKey("anthropic", nil); got != "" {
		t.Fatalf("GetEnvAPIKey(auth token only) = %q", got)
	}

	t.Setenv(AnthropicAuthTokenEnv, "")
	t.Setenv(AnthropicOAuthTokenEnv, "oauth-token")
	if got := FindEnvKeys("anthropic", nil); !slices.Equal(got, []string{"ANTHROPIC_OAUTH_TOKEN"}) {
		t.Fatalf("FindEnvKeys(oauth token) = %v", got)
	}
	if got := GetEnvAPIKey("anthropic", nil); got != "oauth-token" {
		t.Fatalf("GetEnvAPIKey(oauth token) = %q", got)
	}

	t.Setenv(AnthropicOAuthTokenEnv, "")
	t.Setenv(AnthropicAPIKeyEnv, "api-key")
	if got := GetEnvAPIKey("anthropic", nil); got != "api-key" {
		t.Fatalf("GetEnvAPIKey(api key) = %q", got)
	}
}

func TestEnvAPIKeysQwenIndividualReusesInternationalTokenPlanVariable(t *testing.T) {
	clearEnvKeyVars(t)
	got := FindEnvKeys("qwen-token-plan-individual", map[string]string{"QWEN_TOKEN_PLAN_API_KEY": "test"})
	if !slices.Equal(got, []string{"QWEN_TOKEN_PLAN_API_KEY"}) {
		t.Fatalf("FindEnvKeys = %v", got)
	}
}

// TestEnvAPIKeysTableMatchesUpstream checks every entry of upstream
// getApiKeyEnvVars, including the entries PiG previously resolved nowhere
// (meta, qwen-token-plan*, ant-ling, baseten, nvidia, zai-coding-cn).
func TestEnvAPIKeysTableMatchesUpstream(t *testing.T) {
	want := map[string][]string{
		"github-copilot":             {"COPILOT_GITHUB_TOKEN"},
		"anthropic":                  {"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"},
		"ant-ling":                   {"ANT_LING_API_KEY"},
		"qwen-token-plan":            {"QWEN_TOKEN_PLAN_API_KEY"},
		"qwen-token-plan-cn":         {"QWEN_TOKEN_PLAN_CN_API_KEY"},
		"qwen-token-plan-individual": {"QWEN_TOKEN_PLAN_API_KEY"},
		"openai":                     {"OPENAI_API_KEY"},
		"azure-openai-responses":     {"AZURE_OPENAI_API_KEY"},
		"nvidia":                     {"NVIDIA_API_KEY"},
		"deepseek":                   {"DEEPSEEK_API_KEY"},
		"google":                     {"GEMINI_API_KEY"},
		"google-vertex":              {"GOOGLE_CLOUD_API_KEY"},
		"groq":                       {"GROQ_API_KEY"},
		"cerebras":                   {"CEREBRAS_API_KEY"},
		"xai":                        {"XAI_API_KEY"},
		"radius":                     {"RADIUS_API_KEY"},
		"openrouter":                 {"OPENROUTER_API_KEY"},
		"vercel-ai-gateway":          {"AI_GATEWAY_API_KEY"},
		"zai":                        {"ZAI_API_KEY"},
		"zai-coding-cn":              {"ZAI_CODING_CN_API_KEY"},
		"mistral":                    {"MISTRAL_API_KEY"},
		"minimax":                    {"MINIMAX_API_KEY"},
		"minimax-cn":                 {"MINIMAX_CN_API_KEY"},
		"moonshotai":                 {"MOONSHOT_API_KEY"},
		"moonshotai-cn":              {"MOONSHOT_API_KEY"},
		"huggingface":                {"HF_TOKEN"},
		"fireworks":                  {"FIREWORKS_API_KEY"},
		"together":                   {"TOGETHER_API_KEY"},
		"baseten":                    {"BASETEN_API_KEY"},
		"opencode":                   {"OPENCODE_API_KEY"},
		"opencode-go":                {"OPENCODE_API_KEY"},
		"kimi-coding":                {"KIMI_API_KEY"},
		"meta":                       {"META_API_KEY"},
		"cloudflare-workers-ai":      {"CLOUDFLARE_API_KEY"},
		"cloudflare-ai-gateway":      {"CLOUDFLARE_API_KEY"},
		"xiaomi":                     {"XIAOMI_API_KEY"},
		"xiaomi-token-plan-cn":       {"XIAOMI_TOKEN_PLAN_CN_API_KEY"},
		"xiaomi-token-plan-ams":      {"XIAOMI_TOKEN_PLAN_AMS_API_KEY"},
		"xiaomi-token-plan-sgp":      {"XIAOMI_TOKEN_PLAN_SGP_API_KEY"},
	}
	if len(envAPIKeyVars) != len(want)-2 {
		t.Fatalf("env table has %d single-variable entries, want %d", len(envAPIKeyVars), len(want)-2)
	}
	for provider, vars := range want {
		if got := getAPIKeyEnvVars(provider); !slices.Equal(got, vars) {
			t.Errorf("%s: vars = %v, want %v", provider, got, vars)
		}
	}
	for _, provider := range []string{"openai-codex", "amazon-bedrock", "unknown"} {
		if got := getAPIKeyEnvVars(provider); got != nil {
			t.Errorf("%s: vars = %v, want none", provider, got)
		}
	}

	clearEnvKeyVars(t)
	for provider, vars := range want {
		if provider == "anthropic" {
			continue
		}
		t.Setenv(vars[0], provider+"-key")
		if got := GetEnvAPIKey(provider, nil); got != provider+"-key" {
			t.Errorf("%s: GetEnvAPIKey = %q", provider, got)
		}
		t.Setenv(vars[0], "")
		if got := GetEnvAPIKey(provider, map[string]string{vars[0]: "scoped"}); got != "scoped" {
			t.Errorf("%s: scoped GetEnvAPIKey = %q", provider, got)
		}
	}
}

func TestEnvAPIKeysAmbientCredentials(t *testing.T) {
	clearEnvKeyVars(t)
	if got := GetEnvAPIKey("amazon-bedrock", nil); got != "" {
		t.Fatalf("bedrock without credentials = %q", got)
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	if got := GetEnvAPIKey("amazon-bedrock", nil); got != "" {
		t.Fatalf("bedrock with half a key pair = %q", got)
	}
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	if got := GetEnvAPIKey("amazon-bedrock", nil); got != "<authenticated>" {
		t.Fatalf("bedrock with key pair = %q", got)
	}
	if got := GetEnvAPIKey("amazon-bedrock", map[string]string{}); got != "<authenticated>" {
		t.Fatalf("bedrock with empty scoped env = %q", got)
	}
	if got := FindEnvKeys("amazon-bedrock", nil); got != nil {
		t.Fatalf("ambient AWS credentials reported as API keys: %v", got)
	}

	adc := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(adc, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	vertexEnv := map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": adc, "GCLOUD_PROJECT": "p"}
	if got := GetEnvAPIKey("google-vertex", vertexEnv); got != "" {
		t.Fatalf("vertex without location = %q", got)
	}
	vertexEnv["GOOGLE_CLOUD_LOCATION"] = "us-central1"
	if got := GetEnvAPIKey("google-vertex", vertexEnv); got != "<authenticated>" {
		t.Fatalf("vertex ADC = %q", got)
	}
	vertexEnv["GOOGLE_APPLICATION_CREDENTIALS"] = filepath.Join(t.TempDir(), "missing.json")
	if got := GetEnvAPIKey("google-vertex", vertexEnv); got != "" {
		t.Fatalf("vertex with missing credentials file = %q", got)
	}
	vertexEnv["GOOGLE_CLOUD_API_KEY"] = "vertex-key"
	if got := GetEnvAPIKey("google-vertex", vertexEnv); got != "vertex-key" {
		t.Fatalf("vertex API key = %q", got)
	}
}

func TestEnvAPIKeysVertexProcessDiscoveryIsCached(t *testing.T) {
	clearEnvKeyVars(t)
	path := filepath.Join(t.TempDir(), "adc.json")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	if hasVertexADCCredentials(nil) {
		t.Fatal("missing process credential must be unavailable")
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasVertexADCCredentials(nil) {
		t.Fatal("process discovery must retain its first result")
	}
	if !hasVertexADCCredentials(map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": path}) {
		t.Fatal("explicit scoped credential must bypass the process cache")
	}
}

func BenchmarkGetEnvAPIKey(b *testing.B) {
	env := map[string]string{"META_API_KEY": "benchmark-key"}
	b.ReportAllocs()
	for b.Loop() {
		if GetEnvAPIKey("meta", env) != "benchmark-key" {
			b.Fatal("missing key")
		}
	}
}
