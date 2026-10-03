package ai

import (
	"encoding/json"
	"testing"
)

// anthropicStrictTestCapture mirrors captureFirstTool in
// .upstream/v0.99.2/packages/ai/test/anthropic-strict-tool-schema.test.ts:48-68.
func anthropicStrictTestCapture(t *testing.T, tool ToolSchema) map[string]any {
	t.Helper()
	provider := NewAnthropicProvider(AnthropicConfig{
		Model: "claude-opus-4-8", ProviderID: "test-anthropic", BaseURL: "http://127.0.0.1:9", APIKey: "test-key",
		Compat: &AnthropicMessagesCompat{ForceAdaptiveThinking: new(true), SupportsStrictTools: new(true)},
	})
	payload := captureAnthropicUpstreamPayload(t, provider, Context{
		Messages: []Message{UserMessage{Content: UserText("Use the tool")}},
		Tools:    []ToolSchema{tool},
	}, StreamOptions{CacheRetention: CacheRetentionNone})
	var tools []map[string]any
	if err := json.Unmarshal(payload["tools"], &tools); err != nil || len(tools) == 0 {
		t.Fatalf("Expected a tool in the captured Anthropic payload: %s (%v)", payload["tools"], err)
	}
	return tools[0]
}

// anthropicStrictTool decodes a tool through JSON so TypeBox's property
// enumeration order survives the public tool boundary.
func anthropicStrictTool(t *testing.T, parameters string, strict bool) ToolSchema {
	t.Helper()
	document := `{"name":"lookup","description":"Look up a value","parameters":` + parameters
	if strict {
		document += `,"constrainedSampling":{"type":"json_schema","strict":"prefer"}`
	}
	var tool ToolSchema
	if err := json.Unmarshal([]byte(document+`}`), &tool); err != nil {
		t.Fatal(err)
	}
	return tool
}

// .upstream/v0.99.2/packages/ai/test/anthropic-strict-tool-schema.test.ts (moved from
// anthropic-eager-tool-input-compat.test.ts in 0.99.2).
func TestAnthropicStrictToolSchemas(t *testing.T) {
	// anthropic-strict-tool-schema.test.ts:70-104
	t.Run("only sends the full input schema for strict JSON-schema tools", func(t *testing.T) {
		legacy := anthropicStrictTestCapture(t, anthropicStrictTool(t, `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false,"title":"LookupInput"}`, false))
		if _, ok := legacy["strict"]; ok {
			t.Fatalf("legacy tool carries strict: %v", legacy)
		}
		legacySchema, _ := json.Marshal(legacy["input_schema"])
		assertShapeJSON(t, legacySchema, `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`)

		strict := anthropicStrictTestCapture(t, anthropicStrictTool(t, `{"type":"object","properties":{"value":{"type":"string"},"optional":{"type":"number"}},"required":["value"],"title":"StrictLookupInput"}`, true))
		if strict["strict"] != true {
			t.Fatalf("strict tool = %v", strict)
		}
		strictSchema, _ := json.Marshal(strict["input_schema"])
		assertShapeJSON(t, strictSchema, `{"type":"object","properties":{"value":{"type":"string"},"optional":{"anyOf":[{"type":"number"},{"type":"null"}]}},"required":["value","optional"],"title":"StrictLookupInput","additionalProperties":false}`)
	})

	// anthropic-strict-tool-schema.test.ts:106-123 (#9953)
	t.Run("sends prefer tools non-strict when they use keywords Anthropic strict mode rejects", func(t *testing.T) {
		for name, parameters := range map[string]string{
			"minimum and maximum": `{"type":"object","properties":{"timeoutMs":{"type":"integer","minimum":1,"maximum":300000}}}`,
			"minItems above one":  `{"type":"object","properties":{"options":{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"},"minItems":2}},"required":["tags"]}},"required":["options"]}`,
			"unknown format":      `{"type":"object","properties":{"expression":{"type":"string","format":"regex"}},"required":["expression"]}`,
		} {
			t.Run(name, func(t *testing.T) {
				tool := anthropicStrictTestCapture(t, anthropicStrictTool(t, parameters, true))
				if _, ok := tool["strict"]; ok {
					t.Fatalf("tool with rejected keywords is strict: %v", tool)
				}
			})
		}

		supported := anthropicStrictTestCapture(t, anthropicStrictTool(t, `{"type":"object","properties":{"code":{"type":"string","minLength":1,"maxLength":1000,"pattern":"^[a-z]+$"},"url":{"type":"string","format":"uri"},"tags":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["code","url","tags"]}`, true))
		if supported["strict"] != true {
			t.Fatalf("tool with supported keywords = %v, want strict", supported)
		}
	})
}
