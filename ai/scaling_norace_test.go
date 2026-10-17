//go:build !race && linux && !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// toolSizes are the declared-tool counts N, 10N and 100N of the tool scaling gates.
var toolSizes = [3]int{10, 100, 1000}

// declaredTools is count tools shaped like gateToolSchema, each under its own name.
func declaredTools(t testing.TB, count int) []ToolSchema {
	t.Helper()
	tools := make([]ToolSchema, count)
	for i := range tools {
		if err := json.Unmarshal([]byte(gateToolSchema), &tools[i]); err != nil {
			t.Fatal(err)
		}
		tools[i].Name = fmt.Sprintf("tool_%d", i)
	}
	return tools
}

// The per-turn token estimates encode every declared tool once, so their cost is linear in the tools (tool_schema_marshal is the cost of one).
func TestToolEstimateScalingGate(t *testing.T) {
	perfgate.CheckScaling(t, "ai.tool_estimate", toolSizes, func(count int) func() {
		tools := declaredTools(t, count)
		return func() { JSONStringifyLength(tools) }
	})
}

// A request declares every tool in its body: the provider converts and encodes each one. The cost of a whole streamed request grows with the tools by their own conversion and encoding and by nothing else, so the exponent is that of one pass over them. The shape is the openai-completions frame shape the other provider gates use.
func TestProviderRequestToolsScalingGate(t *testing.T) {
	shape := streamFrameShapes[0]
	body := shape.body(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	provider := shape.provider(server.URL)
	perfgate.CheckScaling(t, "ai.request_tools", toolSizes, func(count int) func() {
		context := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("go")}}, Tools: declaredTools(t, count), Frozen: true})
		return func() {
			stream, err := provider.Stream(t.Context(), context, StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(t.Context()) {
			}
		}
	})
}
