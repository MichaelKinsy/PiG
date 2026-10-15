package ai

import (
	"context"
	"maps"
	"slices"
	"testing"
)

// providers/cloudflare-auth.ts: cloudflareWorkersAIAuth and cloudflareAIGatewayAuth merge the API key, account id and (gateway only)
// gateway id per field, the stored credential first and the ambient environment second; any missing value leaves auth unresolved; the
// source is "stored credential" when a credential was given and CLOUDFLARE_API_KEY otherwise; Workers AI sends the key as the API key,
// AI Gateway as `cf-aig-authorization: Bearer <key>` with Authorization and x-api-key removed; login prompts for the key (secret), the
// account id and, for the gateway, the gateway id.
func TestCloudflareWorkersAIAuthMergesCredentialAndEnvironmentPerField(t *testing.T) {
	auth := cloudflareWorkersAIAuth()
	resolve := func(credential *Credential, env map[string]string) *AuthResult {
		t.Helper()
		result, err := auth.Resolve(context.Background(), APIKeyAuthInput{Ctx: testAuthContext(env), Credential: credential})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if auth.Name != "Cloudflare API key" {
		t.Fatalf("name = %q", auth.Name)
	}

	// A credential carrying only the key still picks up the account id from the environment.
	got := resolve(&Credential{Type: CredentialAPIKey, Key: "stored"}, map[string]string{cloudflareAPIKey: "ambient", cloudflareAccountID: "acct"})
	if got == nil || got.Auth.APIKey != "stored" || got.Auth.Headers != nil || got.Source != "stored credential" || !maps.Equal(got.Env, map[string]string{cloudflareAccountID: "acct"}) {
		t.Fatalf("credential key + ambient account = %+v", got)
	}
	// The credential's account id wins over the environment's, and the gateway id is not part of Workers AI.
	got = resolve(&Credential{Type: CredentialAPIKey, Key: "k", Env: map[string]string{cloudflareAccountID: "stored-acct", cloudflareGatewayID: "gw"}}, map[string]string{cloudflareAccountID: "ambient-acct"})
	if got == nil || got.Env[cloudflareAccountID] != "stored-acct" || len(got.Env) != 1 {
		t.Fatalf("credential account id = %+v", got)
	}
	// With no credential everything comes from the environment and the source names the key variable.
	got = resolve(nil, map[string]string{cloudflareAPIKey: "ambient", cloudflareAccountID: "acct"})
	if got == nil || got.Auth.APIKey != "ambient" || got.Source != cloudflareAPIKey {
		t.Fatalf("ambient = %+v", got)
	}
	if got := resolve(nil, map[string]string{cloudflareAPIKey: "ambient"}); got != nil {
		t.Fatalf("no account id resolved: %+v", got)
	}
	if got := resolve(nil, map[string]string{cloudflareAccountID: "acct"}); got != nil {
		t.Fatalf("no key resolved: %+v", got)
	}
}

func TestCloudflareAIGatewayAuthSendsTheGatewayAuthorizationHeader(t *testing.T) {
	auth := cloudflareAIGatewayAuth()
	resolve := func(credential *Credential, env map[string]string) *AuthResult {
		t.Helper()
		result, err := auth.Resolve(context.Background(), APIKeyAuthInput{Ctx: testAuthContext(env), Credential: credential})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	got := resolve(nil, map[string]string{cloudflareAPIKey: "k", cloudflareAccountID: "acct", cloudflareGatewayID: "gw"})
	if got == nil || got.Auth.APIKey != "" || got.Source != cloudflareAPIKey || !maps.Equal(got.Env, map[string]string{cloudflareAccountID: "acct", cloudflareGatewayID: "gw"}) {
		t.Fatalf("gateway = %+v", got)
	}
	headers := got.Auth.Headers
	if headers["cf-aig-authorization"] == nil || *headers["cf-aig-authorization"] != "Bearer k" {
		t.Fatalf("cf-aig-authorization = %v", headers["cf-aig-authorization"])
	}
	for _, removed := range []string{"Authorization", "x-api-key"} {
		if value, present := headers[removed]; !present || value != nil {
			t.Fatalf("%s must be present and null (removed from the request), got %v present=%t", removed, value, present)
		}
	}
	// A gateway without a gateway id is unconfigured.
	if got := resolve(nil, map[string]string{cloudflareAPIKey: "k", cloudflareAccountID: "acct"}); got != nil {
		t.Fatalf("no gateway id resolved: %+v", got)
	}
	// The gateway id may come from the credential while the key comes from the environment.
	got = resolve(&Credential{Type: CredentialAPIKey, Env: map[string]string{cloudflareGatewayID: "stored-gw"}}, map[string]string{cloudflareAPIKey: "k", cloudflareAccountID: "acct"})
	if got == nil || got.Env[cloudflareGatewayID] != "stored-gw" || got.Source != "stored credential" {
		t.Fatalf("credential gateway id = %+v", got)
	}
}

func TestCloudflareAuthLoginPromptsForKeyAccountAndGateway(t *testing.T) {
	for _, tc := range []struct {
		name     string
		auth     *APIKeyAuth
		answers  []string
		messages []string
		env      map[string]string
	}{
		{"workers-ai", cloudflareWorkersAIAuth(), []string{"key", "acct"}, []string{"Enter Cloudflare API key", "Enter Cloudflare account ID"}, map[string]string{cloudflareAccountID: "acct"}},
		{"ai-gateway", cloudflareAIGatewayAuth(), []string{"key", "acct", "gw"}, []string{"Enter Cloudflare API key", "Enter Cloudflare account ID", "Enter Cloudflare AI Gateway ID"}, map[string]string{cloudflareAccountID: "acct", cloudflareGatewayID: "gw"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var messages []string
			var secrets []bool
			next := 0
			credential, err := tc.auth.Login(context.Background(), AuthInteraction{Prompt: func(_ context.Context, prompt AuthPrompt) (string, error) {
				switch p := prompt.(type) {
				case AuthSecretPrompt:
					messages, secrets = append(messages, p.Message), append(secrets, true)
				case AuthTextPrompt:
					messages, secrets = append(messages, p.Message), append(secrets, false)
				}
				answer := tc.answers[next]
				next++
				return answer, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(messages, tc.messages) || secrets[0] != true || slices.Contains(secrets[1:], true) {
				t.Fatalf("prompts = %v secrets = %v", messages, secrets)
			}
			if credential.Type != CredentialAPIKey || credential.Key != "key" || !maps.Equal(credential.Env, tc.env) {
				t.Fatalf("credential = %+v", credential)
			}
		})
	}
}
