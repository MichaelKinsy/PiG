//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// packages/ai/src/api/bedrock-converse-stream.ts:852-867,908,1128 threads provider-scoped env through both system and message cache capability checks.
func TestBedrockForcedCacheUsesProviderEnvironment(t *testing.T) {
	const profile = "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/example"
	for _, tc := range []struct {
		name      string
		modelID   string
		process   string
		scoped    ProviderEnv
		retention CacheRetention
		cache     bool
	}{
		{"opaque profile default", profile, "", nil, CacheRetentionShort, false},
		{"opaque profile scoped opt-in", profile, "", ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "1"}, CacheRetentionShort, true},
		{"opaque profile scoped opt-out", profile, "1", ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "0"}, CacheRetentionShort, false},
		{"opaque profile ambient opt-in", profile, "1", nil, CacheRetentionShort, true},
		{"opaque profile empty scoped falls back", profile, "1", ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": ""}, CacheRetentionShort, true},
		{"explicit none still wins", profile, "1", ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "1"}, CacheRetentionNone, false},
		{"non-Claude model uses the same force branch", "amazon.nova-lite-v1:0", "", ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "1"}, CacheRetentionShort, true},
		{"unsupported Claude does not use the force branch", "anthropic.claude-3-haiku-20240307-v1:0", "1", ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "1"}, CacheRetentionShort, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateBedrockConfig(t)
			t.Setenv("AWS_BEDROCK_FORCE_CACHE", tc.process)
			t.Setenv("PI_CACHE_RETENTION", "")
			requests := make(chan map[string]json.RawMessage, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				requests <- payload
				w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			}))
			defer server.Close()
			provider := NewBedrockProvider(tc.modelID, server.URL)
			options := StreamOptions{CacheRetention: tc.retention, Env: mergeProviderEnv(tc.scoped, ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"})}
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "system", Messages: []Message{UserMessage{Content: UserText("hello")}}}), options)
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
			if tc.cache {
				assertShapeJSON(t, payload["system"], `[{"text":"system"},{"cachePoint":{"type":"default"}}]`)
				assertShapeJSON(t, payload["messages"], `[{"role":"user","content":[{"text":"hello"},{"cachePoint":{"type":"default"}}]}]`)
			} else {
				assertShapeJSON(t, payload["system"], `[{"text":"system"}]`)
				assertShapeJSON(t, payload["messages"], `[{"role":"user","content":[{"text":"hello"}]}]`)
			}
		})
	}
}
