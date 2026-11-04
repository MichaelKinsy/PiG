//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// bedrock-converse-stream.ts:1206 writes `inputSchema: { json: <the tool's schema> }`, and JSON.stringify writes the schema's members in insertion order. The expected bytes are
// what Pi 1.5.0's provider POSTs to a local server for the same tool (an extension's `obj({ zeta, mode: StringEnum([...]), alpha })`), probed with AWS_BEDROCK_FORCE_HTTP1=1.
func TestBedrockToolConfigKeepsTheAuthoredSchemaOrder(t *testing.T) {
	isolateBedrockConfig(t)
	parameters := chordjson.ObjectOf(
		"type", "object",
		"properties", chordjson.ObjectOf(
			"zeta", chordjson.ObjectOf("type", "string", "description", "z"),
			"mode", ai.StringEnum([]string{"fast", "slow"}, &ai.StringEnumOptions{Description: "how", Default: "fast"}),
			"alpha", chordjson.ObjectOf("type", "number"),
		),
		"required", []string{"zeta", "mode"},
		"additionalProperties", false,
	)
	encoded, err := json.Marshal(map[string]any{"name": "enum_probe", "description": "probe", "parameters": parameters})
	if err != nil {
		t.Fatal(err)
	}
	var tool ai.ToolSchema
	if err := json.Unmarshal(encoded, &tool); err != nil {
		t.Fatal(err)
	}
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
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}, Tools: []ai.ToolSchema{tool}}), ai.StreamOptions{Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}})
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
	const want = `{"tools":[{"toolSpec":{"name":"enum_probe","inputSchema":{"json":{"type":"object","properties":{"zeta":{"type":"string","description":"z"},"mode":{"type":"string","enum":["fast","slow"],"description":"how","default":"fast"},"alpha":{"type":"number"}},"required":["zeta","mode"],"additionalProperties":false}},"description":"probe"}}]}`
	if got := string(payload["toolConfig"]); got != want {
		t.Errorf("toolConfig\n got: %s\nwant: %s", got, want)
	}
}
