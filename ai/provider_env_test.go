package ai

import "testing"

// TestGetProviderEnvValue verifies provider-scoped overrides take precedence
// over the process environment, with fallback when absent or empty. Mirrors
// upstream provider-env.ts getProviderEnvValue.
func TestGetProviderEnvValue(t *testing.T) {
	t.Setenv("PIG_PE_VAR", "from-process")

	if got := getProviderEnvValue("PIG_PE_VAR", ProviderEnv{"PIG_PE_VAR": "from-scope"}); got != "from-scope" {
		t.Errorf("scoped env should win: got %q want from-scope", got)
	}
	if got := getProviderEnvValue("PIG_PE_VAR", nil); got != "from-process" {
		t.Errorf("nil scope should use process env: got %q want from-process", got)
	}
	if got := getProviderEnvValue("PIG_PE_VAR", ProviderEnv{"PIG_PE_VAR": ""}); got != "from-process" {
		t.Errorf("empty scoped value should fall back to process env: got %q want from-process", got)
	}
	if got := getProviderEnvValue("PIG_PE_UNSET", ProviderEnv{"OTHER": "x"}); got != "" {
		t.Errorf("unset var should return empty: got %q", got)
	}
}

// TestResolveCloudflareBaseURL_UsesProviderEnv verifies explicit provider-scoped values replace endpoint placeholders while missing values remain unresolved, as in cloudflare-stream.ts.
func TestResolveCloudflareBaseURL_UsesProviderEnv(t *testing.T) {
	const tmpl = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "from-process")

	// scoped value wins
	got, err := ResolveCloudflareBaseURL("cloudflare-workers-ai", tmpl, ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "scoped-acct"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://api.cloudflare.com/client/v4/accounts/scoped-acct/ai/v1" {
		t.Errorf("scoped env not used: got %q", got)
	}

	// The endpoint wrapper does not consult process.env.
	got, err = ResolveCloudflareBaseURL("cloudflare-workers-ai", tmpl, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != tmpl {
		t.Errorf("nil env resolved a placeholder: got %q", got)
	}

	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	if got, err := ResolveCloudflareBaseURL("cloudflare-workers-ai", tmpl, nil); err != nil || got != tmpl {
		t.Errorf("missing env = %q, %v; want unresolved template", got, err)
	}
}
