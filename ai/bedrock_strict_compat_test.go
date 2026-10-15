package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bedrock-converse-stream.ts:250 `const supportsStrictMode = model.compat?.supportsStrictMode ?? false` and :1151-1157 resolveJsonSchemaStrictSampling:
// a tool with json_schema constrained sampling carries `"strict":true` in its toolSpec only when the model's compat sets supportsStrictMode true. An absent
// compat and `false` both leave the tool without it. The Pi payload oracle covers the strict model only.
func TestBedrockStrictToolSpecFollowsTheModelCompat(t *testing.T) {
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for name, compat := range map[string]*ModelCompat{
		"no compat":                         nil,
		"supportsStrictMode false":          {SupportsStrictMode: new(false)},
		"compat without supportsStrictMode": {},
		"supportsStrictMode true":           {SupportsStrictMode: new(true)},
	} {
		t.Run(name, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"stop"}`))
			}))
			defer server.Close()
			model := &Model{ID: "anthropic.claude-3-haiku-20240307-v1:0", DisplayName: "Claude", Input: []string{"text"}, Capabilities: ModelCapabilities{MaxOutputTokens: 1000, ContextWindow: 10000},
				ProviderMeta: ProviderMetadata{ProviderID: "amazon-bedrock", API: APIBedrockConverseStream, BaseURL: server.URL, Compat: compat}}
			tools := mustTools(t, `[{"name":"lookup","description":"Look up","parameters":{"type":"object","properties":{"value":{"type":"string"}}},"constrainedSampling":{"type":"json_schema"}}]`)
			stream, err := BedrockConverseStreamAPI().Stream(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}, Tools: tools}), StreamOptions{MaxTokens: 100})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(t.Context()) {
			}
			if len(body) == 0 {
				t.Fatal("no request body captured")
			}
			if strict := strings.Contains(string(body), `"strict":true`); strict != (compat != nil && compat.SupportsStrictMode != nil && *compat.SupportsStrictMode) {
				t.Fatalf("strict toolSpec = %t for %s\nbody: %s", strict, name, body)
			}
		})
	}
}
