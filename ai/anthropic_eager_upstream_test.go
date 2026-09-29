package ai

import (
	"encoding/json"
	"testing"
)

// packages/ai/test/anthropic-eager-tool-input-compat.test.ts:131-171.
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
	t.Run("only sends the full input schema for strict JSON-schema tools", func(t *testing.T) {
		legacy := tool
		legacy.Parameters = map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false, "title": "LookupInput"}
		var strict ToolSchema
		// JSON preserves TypeBox's value-before-optional property enumeration through the public tool boundary.
		if err := json.Unmarshal([]byte(`{"name":"lookup","description":"Look up a value","parameters":{"type":"object","properties":{"value":{"type":"string"},"optional":{"type":"number"}},"required":["value"],"title":"StrictLookupInput"},"constrainedSampling":{"type":"json_schema","strict":"prefer"}}`), &strict); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			tool   ToolSchema
			want   string
			strict bool
		}{
			{legacy, `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`, false},
			{strict, `{"type":"object","properties":{"value":{"type":"string"},"optional":{"anyOf":[{"type":"number"},{"type":"null"}]}},"required":["value","optional"],"title":"StrictLookupInput","additionalProperties":false}`, true},
		} {
			got := capture(t, &AnthropicMessagesCompat{ForceAdaptiveThinking: new(true), SupportsStrictTools: new(true)}, []ToolSchema{tc.tool})
			first := got.body["tools"].([]any)[0].(map[string]any)
			if tc.strict && first["strict"] != true {
				t.Fatalf("strict tool=%v", first)
			}
			encoded, err := json.Marshal(first["input_schema"])
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, encoded, tc.want)
		}
	})
}
