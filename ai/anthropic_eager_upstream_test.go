package ai

import "testing"

// packages/ai/test/anthropic-eager-tool-input-compat.test.ts. The strict-schema case moved to
// anthropic_strict_tool_schema_upstream_test.go with Pi 0.99.2.
func TestAnthropicUpstreamEagerToolInputCompat(t *testing.T) {
	tool := ToolSchema{Name: "lookup", Description: "Look up a value", Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}}}
	capture := func(t *testing.T, compat *AnthropicMessagesCompat, tools []ToolSchema) anthropicWireCapture {
		t.Helper()
		cfg := AnthropicConfig{Model: "claude-opus-4-8", ProviderID: "test-anthropic", APIKey: "test-key", Compat: compat}
		got, _ := runAnthropicWire(t, cfg, Context{Messages: []Message{UserMessage{Content: UserText("Use the tool")}}, Tools: tools}, StreamOptions{CacheRetention: CacheRetentionNone}, endTurn)
		return got
	}
	for _, tc := range []struct {
		name  string
		eager bool
		tools []ToolSchema
		beta  string
	}{
		{"sends per-tool eager_input_streaming by default", true, []ToolSchema{tool}, ""},
		{"uses the legacy fine-grained tool streaming beta when eager tool input streaming is disabled", false, []ToolSchema{tool}, "fine-grained-tool-streaming-2025-05-14"},
		{"does not send the legacy fine-grained tool streaming beta when there are no tools", false, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compat := &AnthropicMessagesCompat{ForceAdaptiveThinking: new(true)}
			if !tc.eager {
				compat.SupportsEagerToolInputStreaming = new(false)
			}
			got := capture(t, compat, tc.tools)
			if tc.beta == "" {
				if _, ok := got.header["Anthropic-Beta"]; ok {
					t.Fatalf("unexpected beta: %v", got.header)
				}
			} else if got.header.Get("Anthropic-Beta") != tc.beta {
				t.Fatalf("beta=%q", got.header.Get("Anthropic-Beta"))
			}
			if len(tc.tools) == 0 {
				if _, ok := got.body["tools"]; ok {
					t.Fatalf("unexpected tools: %v", got.body["tools"])
				}
				return
			}
			first := got.body["tools"].([]any)[0].(map[string]any)
			if tc.eager {
				if first["eager_input_streaming"] != true {
					t.Fatalf("tool=%v", first)
				}
			} else if _, ok := first["eager_input_streaming"]; ok {
				t.Fatalf("tool=%v", first)
			}
		})
	}
}
