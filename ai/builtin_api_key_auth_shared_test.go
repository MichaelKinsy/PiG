package ai

import (
	"context"
	"maps"
	"slices"
	"testing"
)

var builtinAPIKeyAuthSink *APIKeyAuth

// perCallBuiltinAPIKeyAuth is builtinAPIKeyAuth as it was when it constructed a method on every call: the reference the shared methods must match.
func perCallBuiltinAPIKeyAuth(providerID string) (*APIKeyAuth, bool) {
	switch providerID {
	case "amazon-bedrock":
		return bedrockAuth(), true
	case "anthropic":
		return anthropicAPIKeyAuth(), true
	case "cloudflare-ai-gateway":
		return cloudflareAIGatewayAuth(), true
	case "cloudflare-workers-ai":
		return cloudflareWorkersAIAuth(), true
	case "google-vertex":
		return vertexAuth(), true
	case "openai-codex":
		return nil, true
	}
	name, ok := builtinAPIKeyNames()[providerID]
	if !ok {
		return nil, false
	}
	return EnvAPIKeyAuth(name, getAPIKeyEnvVars(providerID)...), true
}

// Pi's provider modules construct each built-in provider's auth once; builtinAPIKeyAuth hands every caller that one method, built as the per-call construction built it.
func TestBuiltinAPIKeyAuthIsOneSharedMethodPerProvider(t *testing.T) {
	ids := slices.Sorted(maps.Keys(builtinAPIKeyNames()))
	ids = append(ids, slices.Sorted(maps.Keys(builtinAPIKeyAuthMethods))...)
	env := map[string]string{"AWS_PROFILE": "work", "CLOUDFLARE_API_KEY": "cf", "CLOUDFLARE_ACCOUNT_ID": "acct", "CLOUDFLARE_GATEWAY_ID": "gw", "GOOGLE_CLOUD_API_KEY": "gk"}
	for _, providerID := range ids {
		if vars := getAPIKeyEnvVars(providerID); len(vars) > 0 {
			env[vars[0]] = "key-" + providerID
		}
	}
	input := APIKeyAuthInput{Ctx: AuthContext{
		Env:        func(name string) (string, bool) { value, ok := env[name]; return value, ok },
		FileExists: func(string) bool { return false },
	}}
	for _, providerID := range ids {
		first, ok := builtinAPIKeyAuth(providerID)
		if !ok {
			t.Fatalf("%s: no built-in API-key auth", providerID)
		}
		second, _ := builtinAPIKeyAuth(providerID)
		if first != second {
			t.Fatalf("%s: two calls returned different methods", providerID)
		}
		fresh, _ := perCallBuiltinAPIKeyAuth(providerID)
		if (first == nil) != (fresh == nil) {
			t.Fatalf("%s: shared method nil=%v, per-call construction nil=%v", providerID, first == nil, fresh == nil)
		}
		if first == nil {
			continue
		}
		if first.Name != fresh.Name || (first.Login == nil) != (fresh.Login == nil) || (first.Check == nil) != (fresh.Check == nil) {
			t.Fatalf("%s: shared method %q differs from the per-call construction %q", providerID, first.Name, fresh.Name)
		}
		got, gotErr := first.Resolve(context.Background(), input)
		want, wantErr := fresh.Resolve(context.Background(), input)
		if (gotErr == nil) != (wantErr == nil) || (got == nil) != (want == nil) || (got != nil && (got.Source != want.Source || got.Auth.APIKey != want.Auth.APIKey)) {
			t.Fatalf("%s: shared Resolve = %+v, %v; per-call construction = %+v, %v", providerID, got, gotErr, want, wantErr)
		}
	}
	if auth, ok := builtinAPIKeyAuth("no-such-provider"); ok || auth != nil {
		t.Fatalf("unknown provider: auth %v ok %v", auth, ok)
	}
	if auth, ok := builtinAPIKeyAuth("openai-codex"); !ok || auth != nil {
		t.Fatalf("openai-codex is OAuth-only: auth %v ok %v", auth, ok)
	}
	if allocs := testing.AllocsPerRun(100, func() { builtinAPIKeyAuthSink, _ = builtinAPIKeyAuth("openai") }); allocs != 0 {
		t.Fatalf("builtinAPIKeyAuth allocates %v times per call, want 0", allocs)
	}
}
