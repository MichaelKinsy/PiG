package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Pi's AWS SDK (JS) writes every Bedrock document with JSON.stringify, which keeps member insertion order and keeps an empty member name
// (bedrock-converse-stream.ts toolSpec.inputSchema.json is the tool's `parameters` as authored); smithy-go's document encoder would sort the keys and reject
// an empty one (document/json encoder.go encodeMap). PiG writes the authored JSON through bedrockDocument.MarshalSmithyDocument (bedrock_wire.go).
// The Pi-oracle comparison is TestBedrockRequestBodyMatchesPi (needs Node and Pi's dependencies); this hermetic test pins the same rule with the expected text
// written by hand from the authored schemas.
// mutation-checked: marshalling the lazy document (smithy-go: rejects the empty member name) instead of the authored JSON fails the empty-name case.
func TestBedrockToolInputSchemaKeepsItsAuthoredMemberOrder(t *testing.T) {
	t.Setenv("AWS_BEDROCK_SKIP_AUTH", "1")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, tc := range []struct{ name, schema string }{
		{"authored order", `{"type":"object","properties":{"z":{"type":"string","description":"zed"},"a":{"type":"number"}},"required":["z"]}`},
		{"empty property name", `{"type":"object","properties":{"b":{"type":"string"},"":{"description":"d","type":"string"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"stop"}`))
			}))
			defer server.Close()
			tools := mustTools(t, `[{"name":"lookup","description":"Look up","parameters":`+tc.schema+`}]`)
			model := &Model{ID: "anthropic.claude-sonnet-4-5-20250929-v1:0", DisplayName: "Claude", Input: []string{"text"}, Capabilities: ModelCapabilities{MaxOutputTokens: 1000, ContextWindow: 10000},
				ProviderMeta: ProviderMetadata{ProviderID: "amazon-bedrock", API: APIBedrockConverseStream, BaseURL: server.URL}}
			stream, err := BedrockConverseStreamAPI().StreamSimple(t.Context(), model,
				NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}, Tools: tools}), StreamOptions{MaxTokens: 100})
			if err == nil {
				for range stream.Events(t.Context()) {
				}
			}
			want := `"inputSchema":{"json":` + tc.schema + `}`
			if !strings.Contains(string(body), want) {
				t.Fatalf("request body\n got: %s\nwant it to contain: %s", body, want)
			}
		})
	}
}
