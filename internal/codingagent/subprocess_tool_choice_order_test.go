package codingagent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The tool choice object an extension sends reaches Anthropic with its members in the order the extension wrote them: Pi's
// `params.tool_choice = options.toolChoice` sends the caller's object (anthropic-messages.ts:1587-1593). The bridge hands the object on
// as the JSON text it received.
func TestSubprocessToolChoiceObjectReachesAnthropicInTheOrderSent(t *testing.T) {
	var tools []ai.ToolSchema
	if err := json.Unmarshal([]byte(`[{"name":"read","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}]`), &tools); err != nil {
		t.Fatal(err)
	}
	for _, sent := range []string{`{"type":"tool","name":"read"}`, `{"name":"read","type":"tool"}`} {
		options, err := subprocessStreamOptions(map[string]any{"toolChoice": json.RawMessage(sent)}, ai.StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var body []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"captured"}}`)
		}))
		options.APIKey, options.MaxRetries = "k", new(int)
		model := &ai.Model{ID: "m1", Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 100000, MaxOutputTokens: 1024},
			ProviderMeta: ai.ProviderMetadata{ProviderID: "custom", API: ai.APIAnthropicMessages, BaseURL: server.URL}}
		transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}, Tools: tools})
		stream, err := ai.StreamAnthropic(t.Context(), model, transcript, options)
		if err == nil {
			for range stream.Events(t.Context()) {
			}
		}
		server.Close()
		if want := `"tool_choice":` + sent; !strings.Contains(string(body), want) {
			t.Errorf("the request has no %s: %s", want, body)
		}
	}
}
