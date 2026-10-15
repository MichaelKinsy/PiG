package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's configured-provider snapshot is models.checkAuth (model-runtime.ts:340-355). For an api_key credential it runs the
// provider's resolver (models.ts:651-674 → resolve.ts:82-84):
//   - envApiKeyAuth (auth/helpers.ts:18-28) uses the stored key only when it resolves non-empty (auth-storage.ts read resolves
//     "$VAR" and "!cmd") and otherwise falls back to the env vars;
//   - bedrockAuth.resolve (amazon-bedrock.ts:54-78) needs a stored key or an AWS env signal, so the key-less credential of the
//     "credential-chain" login configures nothing by itself;
//   - resolveCloudflareEnv (cloudflare-auth.ts:31-52) also needs CLOUDFLARE_ACCOUNT_ID.
//
// An unconfigured provider makes Pi's prompt say "No API key found for <provider>." and lists none of its models. Its stored
// status still reads "stored" (getProviderAuthStatus uses the credential list).
func TestStoredCredentialCountsOnlyWhenTheResolverAcceptsItLikePi(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "NOPE_UNSET_FOR_TEST", "CLOUDFLARE_API_KEY", "CLOUDFLARE_ACCOUNT_ID", "CLOUDFLARE_GATEWAY_ID", "GROQ_API_KEY", "AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	type tc struct {
		provider, authJSON string
		env                map[string]string
		want               bool
	}
	cases := []tc{
		{"anthropic", `{"anthropic":{"type":"api_key","key":""}}`, nil, false},
		{"anthropic", `{"anthropic":{"type":"api_key"}}`, nil, false},
		{"anthropic", `{"anthropic":{"type":"api_key","key":"$NOPE_UNSET_FOR_TEST"}}`, nil, false},
		{"anthropic", `{"anthropic":{"type":"api_key","key":""}}`, map[string]string{"ANTHROPIC_API_KEY": "sk-env"}, true},
		{"anthropic", `{"anthropic":{"type":"api_key","key":"sk-stored"}}`, nil, true},
		{"amazon-bedrock", `{"amazon-bedrock":{"type":"api_key"}}`, nil, false},
		{"amazon-bedrock", `{"amazon-bedrock":{"type":"api_key"}}`, map[string]string{"AWS_PROFILE": "default"}, true},
		{"cloudflare-workers-ai", `{"cloudflare-workers-ai":{"type":"api_key","key":"k"}}`, nil, false},
		{"cloudflare-workers-ai", `{"cloudflare-workers-ai":{"type":"api_key","key":"k"}}`, map[string]string{"CLOUDFLARE_ACCOUNT_ID": "a"}, true},
		// checkProviderAuth's first branch: a stored OAuth credential counts only for a provider with an OAuth method, and it
		// hides the provider's env key.
		{"groq", `{"groq":{"type":"oauth","access":"x","refresh":"y","expires":9999999999999}}`, nil, false},
		{"groq", `{"groq":{"type":"oauth","access":"x","refresh":"y","expires":9999999999999}}`, map[string]string{"GROQ_API_KEY": "gsk"}, false},
		{"anthropic", `{"anthropic":{"type":"oauth","access":"x","refresh":"y","expires":9999999999999}}`, nil, true},
		{"github-copilot", `{"github-copilot":{"type":"oauth","access":"x","expires":9999999999999}}`, nil, true},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, tc{"anthropic", `{"anthropic":{"type":"api_key","key":"!false"}}`, nil, false}, tc{"anthropic", `{"anthropic":{"type":"api_key","key":"!echo sk-cmd"}}`, nil, true})
	}
	for _, c := range cases {
		t.Run(c.authJSON+fmtEnv(c.env), func(t *testing.T) {
			for name, value := range c.env {
				t.Setenv(name, value)
			}
			agentDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(c.authJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			registry := NewModelRegistry(agentDir)
			storage, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			registry.SetAuthStorage(storage)
			if got := registry.HasConfiguredAuth(c.provider); got != c.want {
				t.Errorf("HasConfiguredAuth = %v, want %v", got, c.want)
			}
			if got := AuthenticatedProviders(agentDir)[c.provider]; got != c.want {
				t.Errorf("in AuthenticatedProviders = %v, want %v", got, c.want)
			}
			if status := registry.ExtensionProviderAuthStatus(c.provider); !status.Configured || status.Source != "stored" {
				t.Errorf("auth status = %+v, want configured from the stored credential", status)
			}
		})
	}
}

// The env-only check is the provider's resolver too: Cloudflare AI Gateway needs the account and gateway ids with the key
// (cloudflare-auth.ts:31-52), and a stored OAuth credential for a provider without OAuth hides a models.json apiKey
// (models.ts:656-658).
func TestEnvAndModelsJSONAuthFollowPisCheckProviderAuth(t *testing.T) {
	for _, name := range []string{"CLOUDFLARE_API_KEY", "CLOUDFLARE_ACCOUNT_ID", "CLOUDFLARE_GATEWAY_ID"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CLOUDFLARE_API_KEY", "k")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "a")
	if hasEnvAuth("cloudflare-ai-gateway") {
		t.Error("cloudflare-ai-gateway without CLOUDFLARE_GATEWAY_ID counts as env auth")
	}
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "g")
	if !hasEnvAuth("cloudflare-ai-gateway") {
		t.Error("cloudflare-ai-gateway with key, account and gateway does not count as env auth")
	}

	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(`{"myp":{"type":"oauth","access":"x","refresh":"y","expires":9999999999999}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"myp":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","apiKey":"sk","models":[{"id":"m1"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(agentDir)
	storage, err := ai.NewAuthStorage(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry.SetAuthStorage(storage)
	if registry.HasConfiguredAuth("myp") {
		t.Error("a stored OAuth credential for a models.json provider without OAuth counts as configured")
	}
	for _, entry := range registry.GetAvailable() {
		if entry.ProviderID == "myp" {
			t.Errorf("GetAvailable lists %s/%s", entry.ProviderID, entry.ModelID)
		}
	}
}

func fmtEnv(env map[string]string) string {
	out := ""
	for name, value := range env {
		out += " " + name + "=" + value
	}
	return out
}
