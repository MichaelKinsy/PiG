package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// packages/ai/src/api/bedrock-converse-stream.ts:260,815-823 resolves the explicit option before the legacy long-only environment.
func TestBedrockCacheRetentionOptionPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		option     CacheRetention
		processEnv string
		scopedEnv  ProviderEnv
		cache      bool
		ttl        string
	}{
		{"default short", "", "", nil, true, ""},
		{"explicit none beats long", CacheRetentionNone, "long", nil, false, ""},
		{"explicit long beats none", CacheRetentionLong, "none", nil, true, "1h"},
		{"explicit short beats long", CacheRetentionShort, "long", nil, true, ""},
		{"environment long", "", "long", nil, true, "1h"},
		{"environment none defaults short", "", "none", nil, true, ""},
		{"unknown environment defaults short", "", "invalid", nil, true, ""},
		{"scoped long beats process none", "", "none", ProviderEnv{"PI_CACHE_RETENTION": "long"}, true, "1h"},
		// packages/ai/src/utils/provider-env.ts:46 uses truthy fallback for an empty scoped value.
		{"scoped empty falls back to process long", "", "long", ProviderEnv{"PI_CACHE_RETENTION": ""}, true, "1h"},
		{"explicit none beats scoped long", CacheRetentionNone, "", ProviderEnv{"PI_CACHE_RETENTION": "long"}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateBedrockConfig(t)
			t.Setenv("PI_CACHE_RETENTION", tc.processEnv)
			requests := make(chan map[string]json.RawMessage, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				requests <- request
				w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			}))
			defer server.Close()
			provider := NewBedrockProvider("us.anthropic.claude-sonnet-4-5-20250929-v1:0", server.URL)
			env := mergeProviderEnv(tc.scopedEnv, ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "system", Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{CacheRetention: tc.option, Env: env})
			if err != nil {
				t.Fatal(err)
			}
			stream.Result()
			var payload map[string]json.RawMessage
			select {
			case payload = <-requests:
			default:
				t.Fatal("provider settled without sending the request")
			}
			if !tc.cache {
				assertShapeJSON(t, payload["system"], `[{"text":"system"}]`)
				assertShapeJSON(t, payload["messages"], `[{"role":"user","content":[{"text":"hello"}]}]`)
				return
			}
			point := `{"cachePoint":{"type":"default"}}`
			if tc.ttl != "" {
				point = `{"cachePoint":{"type":"default","ttl":"` + tc.ttl + `"}}`
			}
			assertShapeJSON(t, payload["system"], `[{"text":"system"},`+point+`]`)
			assertShapeJSON(t, payload["messages"], `[{"role":"user","content":[{"text":"hello"},`+point+`]}]`)
		})
	}
}
