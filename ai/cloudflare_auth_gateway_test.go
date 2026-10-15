package ai

import (
	"testing"
)

// providers/cloudflare-auth.ts cloudflareWorkersAIAuth.resolve: the API key, account id and ambient env are merged per field (a stored credential value wins, the environment fills the rest), a missing piece resolves to nothing, and the source says where the key came from.
func TestCloudflareWorkersAIAuthMergesPerFieldStoredCredentialWinsOverAmbientEnv(t *testing.T) {
	auth := cloudflareWorkersAIAuth()
	ctx := testAuthContext(map[string]string{"CLOUDFLARE_API_KEY": "env-key", "CLOUDFLARE_ACCOUNT_ID": "env-account"})

	resolved, err := auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: ctx})
	if err != nil || resolved == nil || resolved.Auth.APIKey != "env-key" || resolved.Env["CLOUDFLARE_ACCOUNT_ID"] != "env-account" || resolved.Source != "CLOUDFLARE_API_KEY" {
		t.Fatalf("ambient = %+v, %v", resolved, err)
	}
	if _, hasGateway := resolved.Env["CLOUDFLARE_GATEWAY_ID"]; hasGateway || len(resolved.Auth.Headers) != 0 {
		t.Fatalf("a Workers AI resolution carries no gateway id or headers: %+v", resolved)
	}

	stored := &Credential{Type: CredentialAPIKey, Key: "stored-key"}
	resolved, err = auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: ctx, Credential: stored})
	if err != nil || resolved == nil || resolved.Auth.APIKey != "stored-key" || resolved.Env["CLOUDFLARE_ACCOUNT_ID"] != "env-account" || resolved.Source != "stored credential" {
		t.Fatalf("stored key with ambient account = %+v, %v", resolved, err)
	}

	stored = &Credential{Type: CredentialAPIKey, Key: "stored-key", Env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "stored-account"}}
	if resolved, err = auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: ctx, Credential: stored}); err != nil || resolved == nil || resolved.Env["CLOUDFLARE_ACCOUNT_ID"] != "stored-account" {
		t.Fatalf("stored account must win over the environment: %+v, %v", resolved, err)
	}

	if resolved, err = auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: testAuthContext(map[string]string{"CLOUDFLARE_API_KEY": "env-key"})}); err != nil || resolved != nil {
		t.Fatalf("a missing account id must resolve to nothing: %+v, %v", resolved, err)
	}
}

// providers/cloudflare-auth.ts cloudflareAIGatewayAuth.resolve: the gateway id is required too, and the key travels as cf-aig-authorization with the standard Authorization and x-api-key headers removed.
func TestCloudflareAIGatewayAuthSendsTheKeyAsAGatewayHeaderAndNeedsTheGatewayID(t *testing.T) {
	auth := cloudflareAIGatewayAuth()
	ctx := testAuthContext(map[string]string{"CLOUDFLARE_API_KEY": "env-key", "CLOUDFLARE_ACCOUNT_ID": "acct", "CLOUDFLARE_GATEWAY_ID": "gw"})

	resolved, err := auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: ctx})
	if err != nil || resolved == nil {
		t.Fatalf("resolve = %+v, %v", resolved, err)
	}
	headers := resolved.Auth.Headers
	if value, ok := headers["cf-aig-authorization"]; !ok || value == nil || *value != "Bearer env-key" {
		t.Errorf("cf-aig-authorization = %v", headers["cf-aig-authorization"])
	}
	for _, removed := range []string{"Authorization", "x-api-key"} {
		if value, ok := headers[removed]; !ok || value != nil {
			t.Errorf("%s must be present and null to remove it, got %v (present %v)", removed, value, ok)
		}
	}
	if resolved.Auth.APIKey != "" || resolved.Env["CLOUDFLARE_ACCOUNT_ID"] != "acct" || resolved.Env["CLOUDFLARE_GATEWAY_ID"] != "gw" || resolved.Source != "CLOUDFLARE_API_KEY" {
		t.Errorf("resolution = %+v", resolved)
	}

	withoutGateway := testAuthContext(map[string]string{"CLOUDFLARE_API_KEY": "env-key", "CLOUDFLARE_ACCOUNT_ID": "acct"})
	if resolved, err = auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: withoutGateway}); err != nil || resolved != nil {
		t.Fatalf("a missing gateway id must resolve to nothing: %+v, %v", resolved, err)
	}
}
