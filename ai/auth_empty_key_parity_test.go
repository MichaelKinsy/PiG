package ai

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"
)

// Pi cloudflare-auth.ts:19-29 distinguishes undefined from empty/null; google-vertex.ts:71-74 uses nullish coalescing. Truthy-key helpers intentionally have different fallback semantics.
func TestProviderEmptyKeyFallbackMatchesPi(t *testing.T) {
	cases := []struct {
		provider, credential string
		env                  map[string]string
	}{
		{"cloudflare-workers-ai", `{"type":"api_key","key":"","env":{"CLOUDFLARE_ACCOUNT_ID":"account"}}`, map[string]string{"CLOUDFLARE_API_KEY": "ambient"}},
		{"cloudflare-ai-gateway", `{"type":"api_key","key":"","env":{"CLOUDFLARE_ACCOUNT_ID":"account","CLOUDFLARE_GATEWAY_ID":"gateway"}}`, map[string]string{"CLOUDFLARE_API_KEY": "ambient"}},
		{"cloudflare-workers-ai", `{"type":"api_key","env":{"CLOUDFLARE_ACCOUNT_ID":"account"}}`, map[string]string{"CLOUDFLARE_API_KEY": "ambient"}},
		{"cloudflare-workers-ai", `{"type":"api_key","key":null,"env":{"CLOUDFLARE_ACCOUNT_ID":"account"}}`, map[string]string{"CLOUDFLARE_API_KEY": "ambient"}},
		{"google-vertex", `{"type":"api_key","key":""}`, map[string]string{"GOOGLE_CLOUD_API_KEY": "ambient"}},
		{"google-vertex", `{"type":"api_key"}`, map[string]string{"GOOGLE_CLOUD_API_KEY": "ambient"}},
		{"google-vertex", `{"type":"api_key","key":null}`, map[string]string{"GOOGLE_CLOUD_API_KEY": "ambient"}},
		{"openai", `{"type":"api_key","key":""}`, map[string]string{"OPENAI_API_KEY": "ambient"}},
		{"amazon-bedrock", `{"type":"api_key","key":""}`, map[string]string{"AWS_BEARER_TOKEN_BEDROCK": "ambient"}},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.credential, func(t *testing.T) {
			input, err := json.Marshal(map[string]any{"provider": tc.provider, "credential": json.RawMessage(tc.credential), "env": tc.env})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), "node", "ai/testdata/provider-auth-empty-key-pi.mjs")
			command.Dir = ".."
			command.Stdin = bytes.NewReader(input)
			oracle, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("Pi: %v\n%s", err, oracle)
			}
			var want any
			if err := json.Unmarshal(oracle, &want); err != nil {
				t.Fatal(err)
			}
			var credential Credential
			if err := json.Unmarshal([]byte(tc.credential), &credential); err != nil {
				t.Fatal(err)
			}
			auth, err := BuiltinProviderAuth(tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			calls := []string{}
			result, err := auth.APIKey.Resolve(t.Context(), APIKeyAuthInput{Credential: &credential, Ctx: AuthContext{
				Env: func(name string) (string, bool) {
					calls = append(calls, "env:"+name)
					value, ok := tc.env[name]
					return value, ok
				},
				FileExists: func(path string) bool { calls = append(calls, "file:"+path); return false },
			}})
			if err != nil {
				t.Fatal(err)
			}
			var projected any
			if result != nil {
				authFields := map[string]any{}
				if result.Auth.APIKey != "" {
					authFields["apiKey"] = result.Auth.APIKey
				}
				if result.Auth.Headers != nil {
					authFields["headers"] = result.Auth.Headers
				}
				if result.Auth.BaseURL != "" {
					authFields["baseUrl"] = result.Auth.BaseURL
				}
				fields := map[string]any{"auth": authFields}
				if result.Env != nil {
					fields["env"] = result.Env
				}
				if result.Source != "" {
					fields["source"] = result.Source
				}
				projected = fields
			}
			encoded, err := json.Marshal(map[string]any{"result": projected, "calls": calls})
			if err != nil {
				t.Fatal(err)
			}
			var got any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Go: %s\nPi: %s", encoded, oracle)
			}
		})
	}
}
