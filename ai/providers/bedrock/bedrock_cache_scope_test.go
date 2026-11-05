//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"github.com/MichaelKinsy/PiG/ai"

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
		scoped    ai.ProviderEnv
		retention ai.CacheRetention
		cache     bool
	}{
		{"opaque profile default", profile, "", nil, ai.CacheRetentionShort, false},
		{"opaque profile scoped opt-in", profile, "", ai.ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "1"}, ai.CacheRetentionShort, true},
		{"opaque profile scoped opt-out", profile, "1", ai.ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "0"}, ai.CacheRetentionShort, false},
		{"opaque profile ambient opt-in", profile, "1", nil, ai.CacheRetentionShort, true},
		{"opaque profile empty scoped falls back", profile, "1", ai.ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": ""}, ai.CacheRetentionShort, true},
		{"explicit none still wins", profile, "1", ai.ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "1"}, ai.CacheRetentionNone, false},
		{"non-Claude model uses the same force branch", "amazon.nova-lite-v1:0", "", ai.ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "1"}, ai.CacheRetentionShort, true},
		{"unsupported Claude does not use the force branch", "anthropic.claude-3-haiku-20240307-v1:0", "1", ai.ProviderEnv{"AWS_BEDROCK_FORCE_CACHE": "1"}, ai.CacheRetentionShort, false},
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
			options := ai.StreamOptions{CacheRetention: tc.retention, Env: ai.MergeProviderEnv(tc.scoped, ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"})}
			stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{SystemPrompt: "system", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), options)
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
