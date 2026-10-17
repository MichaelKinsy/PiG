package codingagent

import "testing"

// Pi's configured-provider snapshot is each provider's auth check (model-runtime.ts:340-355). Cloudflare's check
// (cloudflare-auth.ts resolveCloudflareEnv) needs CLOUDFLARE_API_KEY and CLOUDFLARE_ACCOUNT_ID, plus CLOUDFLARE_GATEWAY_ID for
// AI Gateway; a key alone configures neither provider, so the prompt says "No API key found" and --list-models lists none.
func TestCloudflareEnvAuthNeedsTheAccountAndGatewayLikePi(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_KEY", "key")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "")
	check := func(workers, gateway bool) {
		t.Helper()
		agentDir := t.TempDir()
		registry := NewModelRegistry(agentDir)
		authed := AuthenticatedProviders(agentDir)
		for provider, want := range map[string]bool{"cloudflare-workers-ai": workers, "cloudflare-ai-gateway": gateway} {
			if got := registry.HasConfiguredAuth(provider); got != want {
				t.Errorf("%s HasConfiguredAuth = %v, want %v", provider, got, want)
			}
			if got := authed[provider]; got != want {
				t.Errorf("%s in AuthenticatedProviders = %v, want %v", provider, got, want)
			}
		}
	}
	check(false, false)
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "account")
	check(true, false)
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "gateway")
	check(true, true)
}
