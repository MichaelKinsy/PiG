package codingagent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// writeAuthJSON writes a minimal auth.json fixture under dir.
func writeAuthJSON(t *testing.T, dir string, contents map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(contents)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// clearAllAuthEnv unsets the env vars AuthenticatedProviders inspects so
// the test environment doesn't leak the developer's keys into the
// "no auth" assertion.
func clearAllAuthEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"OPENAI_API_KEY", "OPENROUTER_API_KEY", "GROQ_API_KEY", "OLLAMA_HOST",
		"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "AZURE_OPENAI_API_KEY", "AZURE_OPENAI_BASE_URL",
		"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_CLOUD_API_KEY", "GOOGLE_CLOUD_PROJECT",
		"GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION", "MISTRAL_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS",
		"DEEPSEEK_API_KEY", "CEREBRAS_API_KEY", "XAI_API_KEY", "AI_GATEWAY_API_KEY",
		"ZAI_API_KEY", "MINIMAX_API_KEY", "MINIMAX_CN_API_KEY", "MOONSHOT_API_KEY",
		"HF_TOKEN", "FIREWORKS_API_KEY", "OPENCODE_API_KEY", "KIMI_API_KEY", "CLOUDFLARE_API_KEY",
		"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
		"AWS_PROFILE", "AWS_SESSION_TOKEN", "AWS_WEB_IDENTITY_TOKEN_FILE",
	} {
		t.Setenv(v, "")
	}
}

func TestAuthenticatedProviders_NoAuth_EmptySet(t *testing.T) {
	clearAllAuthEnv(t)
	// Use a temp HOME so a developer's ~/.aws/{credentials,config}
	// can't flip amazon-bedrock to authenticated under this test.
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if len(got) != 0 {
		t.Fatalf("expected empty set with no creds, got %v", got)
	}
}

func TestAuthenticatedProviders_CopilotOAuth_IncludesCopilot(t *testing.T) {
	clearAllAuthEnv(t)
	dir := t.TempDir()
	// Future-dated OAuth credential.
	writeAuthJSON(t, dir, map[string]any{
		"github-copilot": map[string]any{
			"type":    "oauth",
			"refresh": "rtok",
			"access":  "atok",
			"expires": time.Now().Add(24 * time.Hour).UnixMilli(),
		},
	})
	got := AuthenticatedProviders(dir)
	if !got["github-copilot"] {
		t.Errorf("expected github-copilot ∈ AuthenticatedProviders, got %v", got)
	}
}

func TestAuthenticatedProviders_CopilotNoRefreshToken_Excluded(t *testing.T) {
	clearAllAuthEnv(t)
	dir := t.TempDir()
	// OAuth credential with no refresh token (e.g. half-completed login).
	writeAuthJSON(t, dir, map[string]any{
		"github-copilot": map[string]any{
			"type":    "oauth",
			"access":  "atok",
			"expires": time.Now().Add(24 * time.Hour).UnixMilli(),
		},
	})
	got := AuthenticatedProviders(dir)
	if got["github-copilot"] {
		t.Errorf("expected copilot OAuth without refresh token excluded, got %v", got)
	}
}

func TestAuthenticatedProviders_OpenAIEnvVar_Includes(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["openai"] {
		t.Errorf("expected openai ∈ AuthenticatedProviders via env, got %v", got)
	}
	if got["openai-codex"] {
		t.Errorf("openai-codex must stay OAuth-only in list-models auth detection, got %v", got)
	}
}

func TestAuthenticatedProviders_AnthropicEnvVar_Includes(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["anthropic"] {
		t.Errorf("expected anthropic ∈ AuthenticatedProviders via env, got %v", got)
	}
}

func TestAuthenticatedProviders_AzureEnvVar_Includes(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("AZURE_OPENAI_API_KEY", "sk-azure")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["azure-openai-responses"] {
		t.Errorf("expected azure-openai-responses ∈ AuthenticatedProviders via env, got %v", got)
	}
}

func TestAuthenticatedProviders_GoogleEnvVar_Includes(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("GEMINI_API_KEY", "sk-google")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["google"] {
		t.Errorf("expected google ∈ AuthenticatedProviders via env, got %v", got)
	}
}

func TestAuthenticatedProviders_GoogleAPIKeyOnly_Excluded(t *testing.T) {
	// Upstream envMap maps google → GEMINI_API_KEY only.
	// GOOGLE_API_KEY must NOT authenticate the google provider.
	clearAllAuthEnv(t)
	t.Setenv("GOOGLE_API_KEY", "sk-google-old")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if got["google"] {
		t.Errorf("GOOGLE_API_KEY must not authenticate google provider (upstream uses GEMINI_API_KEY only), got %v", got)
	}
}

func TestAuthenticatedProviders_BedrockContainerCreds_Includes(t *testing.T) {
	// Upstream checks AWS_CONTAINER_CREDENTIALS_RELATIVE_URI and
	// AWS_CONTAINER_CREDENTIALS_FULL_URI for ECS task role support.
	clearAllAuthEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "/v2/credentials/xxx")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["amazon-bedrock"] {
		t.Errorf("expected amazon-bedrock via AWS_CONTAINER_CREDENTIALS_RELATIVE_URI, got %v", got)
	}
}

func TestAuthenticatedProviders_GoogleVertexEnvVar_Includes(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("GOOGLE_CLOUD_API_KEY", "sk-vertex")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["google-vertex"] {
		t.Errorf("expected google-vertex ∈ AuthenticatedProviders via env, got %v", got)
	}
}

func TestAuthenticatedProviders_GoogleVertexADC_Includes(t *testing.T) {
	// Upstream caches ambient ADC discovery per process. Start with an empty cache.
	if os.Getenv("PIG_TEST_ADC_CHILD") != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestAuthenticatedProviders_GoogleVertexADC_Includes$", "-test.count=1")
		cmd.Env = append(os.Environ(), "PIG_TEST_ADC_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ADC subprocess: %v\n%s", err, output)
		}
		return
	}
	clearAllAuthEnv(t)
	home := t.TempDir()
	// Upstream reads homedir()/.config/gcloud on every OS; Windows takes the
	// home directory from USERPROFILE.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "proj-1")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
	adcPath := filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	if err := os.MkdirAll(filepath.Dir(adcPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(adcPath, []byte(`{"client_id":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["google-vertex"] {
		t.Errorf("expected google-vertex ∈ AuthenticatedProviders via ADC, got %v", got)
	}
}

func TestAuthenticatedProviders_MistralEnvVar_Includes(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("MISTRAL_API_KEY", "sk-mistral")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["mistral"] {
		t.Errorf("expected mistral ∈ AuthenticatedProviders via env, got %v", got)
	}
}

func TestAuthenticatedProviders_AdditionalEnvProviders_AreDetected(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek")
	t.Setenv("MOONSHOT_API_KEY", "sk-moonshot")
	t.Setenv("CLOUDFLARE_API_KEY", "sk-cloudflare")
	dir := t.TempDir()
	registry := NewModelRegistry(dir)

	cases := []struct {
		provider string
		want     string
	}{
		{provider: "deepseek", want: "sk-deepseek"},
		{provider: "moonshotai", want: "sk-moonshot"},
		{provider: "moonshotai-cn", want: "sk-moonshot"},
		{provider: "cloudflare-ai-gateway", want: "sk-cloudflare"},
	}
	for _, tc := range cases {
		if got := resolveAPIKeyFromEnv(tc.provider); got != tc.want {
			t.Fatalf("resolveAPIKeyFromEnv(%q) = %q, want %q", tc.provider, got, tc.want)
		}
		if !registry.HasAnyKey(tc.provider) {
			t.Fatalf("expected HasAnyKey(%q)=true", tc.provider)
		}
	}
}

func TestAuthenticatedProviders_OpenAICodexOAuth_IncludesProvider(t *testing.T) {
	clearAllAuthEnv(t)
	dir := t.TempDir()
	writeAuthJSON(t, dir, map[string]any{
		"openai-codex": map[string]any{
			"type":    "oauth",
			"refresh": "rtok",
			"access":  "atok",
			"expires": time.Now().Add(24 * time.Hour).UnixMilli(),
		},
	})
	got := AuthenticatedProviders(dir)
	if !got["openai-codex"] {
		t.Errorf("expected openai-codex ∈ AuthenticatedProviders, got %v", got)
	}
}

func TestAuthenticatedProviders_OllamaHostEnv_Includes(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("OLLAMA_HOST", "http://localhost:11434")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["ollama"] {
		t.Errorf("expected ollama ∈ AuthenticatedProviders, got %v", got)
	}
}

func TestAuthenticatedProviders_BedrockAuthDetected(t *testing.T) {
	// amazon-bedrock is now a real reachable provider. When AWS auth
	// signals are present, the picker should include it.
	clearAllAuthEnv(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIATEST")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if !got["amazon-bedrock"] {
		t.Errorf("expected amazon-bedrock ∈ AuthenticatedProviders with AWS_ACCESS_KEY_ID set, got %v", got)
	}
	if !ReachableProviders()["amazon-bedrock"] {
		t.Errorf("amazon-bedrock must be in ReachableProviders")
	}
	// Other catalog-only providers still must NOT leak into reachable.
	for _, banned := range []string{"google", "google-vertex", "azure-openai-responses", "mistral", "cerebras", "fireworks", "xai", "zai"} {
		if ReachableProviders()[banned] {
			t.Errorf("%s leaked into ReachableProviders", banned)
		}
	}
}

func TestAuthenticatedProviders_BedrockNoAuth_Excluded(t *testing.T) {
	// Without AWS auth signals, amazon-bedrock must NOT appear in the
	// authenticated set even though it's reachable. This protects the
	// picker from showing an option that would fail at first request.
	clearAllAuthEnv(t)
	// Use a temp HOME so the user's ~/.aws/{credentials,config} does not
	// leak in and falsely report Bedrock as authenticated.
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	got := AuthenticatedProviders(dir)
	if got["amazon-bedrock"] {
		t.Errorf("amazon-bedrock must not be authenticated without AWS env vars: %v", got)
	}
}

func TestReachableProviders_ExactSet(t *testing.T) {
	want := map[string]bool{
		"github-copilot": true,
		"anthropic":      true,
		"openai":         true,
		"openai-codex":   true,
		"openrouter":     true,
		"groq":           true,
		"ollama":         true,
		"amazon-bedrock": true,
	}
	got := ReachableProviders()
	if len(got) != len(want) {
		t.Fatalf("ReachableProviders size mismatch: want %d, got %d (%v)", len(want), len(got), got)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing %q in ReachableProviders", k)
		}
	}
}
