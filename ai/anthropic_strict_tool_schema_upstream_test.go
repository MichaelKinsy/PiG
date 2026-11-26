package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// anthropicStrictTestCaptureRaw keeps the serialized bytes of the first tool's input_schema so key order is observable.
func anthropicStrictTestCaptureRaw(t *testing.T, tool ToolSchema) (captured struct {
	Strict      *bool           `json:"strict"`
	InputSchema json.RawMessage `json:"input_schema"`
}) {
	t.Helper()
	provider := NewAnthropicProvider(AnthropicConfig{
		Model: "claude-opus-4-8", ProviderID: "test-anthropic", BaseURL: "http://127.0.0.1:9", APIKey: "test-key",
		Compat: &AnthropicMessagesCompat{ForceAdaptiveThinking: new(true), SupportsStrictTools: new(true)},
	})
	payload := captureAnthropicUpstreamPayload(t, provider, Context{
		Messages: []Message{UserMessage{Content: UserText("Use the tool")}},
		Tools:    []ToolSchema{tool},
	}, StreamOptions{CacheRetention: CacheRetentionNone})
	var tools []struct {
		Strict      *bool           `json:"strict"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
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
	// anthropic-strict-tool-schema.test.ts:70-104; the legacy schema expects additionalProperties: false since Pi aa47cedce (#12100)
	t.Run("only sends the full input schema for strict JSON-schema tools", func(t *testing.T) {
		legacy := anthropicStrictTestCapture(t, anthropicStrictTool(t, `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false,"title":"LookupInput"}`, false))
		if _, ok := legacy["strict"]; ok {
			t.Fatalf("legacy tool carries strict: %v", legacy)
		}
		legacySchema, _ := json.Marshal(legacy["input_schema"])
		assertShapeJSON(t, legacySchema, `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)

		strict := anthropicStrictTestCapture(t, anthropicStrictTool(t, `{"type":"object","properties":{"value":{"type":"string"},"optional":{"type":"number"}},"required":["value"],"title":"StrictLookupInput"}`, true))
		if strict["strict"] != true {
			t.Fatalf("strict tool = %v", strict)
		}
		strictSchema, _ := json.Marshal(strict["input_schema"])
		assertShapeJSON(t, strictSchema, `{"type":"object","properties":{"value":{"type":"string"},"optional":{"anyOf":[{"type":"number"},{"type":"null"}]}},"required":["value","optional"],"title":"StrictLookupInput","additionalProperties":false}`)
	})

	// anthropic-strict-tool-schema.test.ts "keeps root definitions in non-strict schemas so property refs
	// resolve" (Pi 4a6a58eb5, #9557). Pi compares with toEqual; the Go port also asserts the exact serialized bytes,
	// because JSON.stringify emits type, properties, required, then $defs, then definitions.
	t.Run("keeps root definitions in non-strict schemas so property refs resolve", func(t *testing.T) {
		settings := `{"type":"object","properties":{"verbose":{"type":"boolean"}}}`
		for _, test := range []struct{ name, ref, definitions, want string }{
			{"$defs", "#/$defs/Settings", `"$defs":{"Settings":` + settings + `}`, ""},
			{"definitions", "#/definitions/Settings", `"definitions":{"Settings":` + settings + `}`, ""},
			{"both", "#/$defs/Settings", `"$defs":{"Settings":` + settings + `},"definitions":{"Settings":` + settings + `}`, ""},
			{"root anyOf stays out", "#/$defs/Settings", `"anyOf":[{"type":"object"}],"oneOf":[{"type":"object"}],"allOf":[{"type":"object"}],"$defs":{"Settings":` + settings + `}`, `"$defs":{"Settings":` + settings + `}`},
		} {
			t.Run(test.name, func(t *testing.T) {
				prefix := `{"type":"object","properties":{"settings":{"$ref":"` + test.ref + `"}},"required":["settings"],`
				want := prefix + test.definitions + `}`
				if test.want != "" {
					want = prefix + test.want + `}`
				}
				tool := anthropicStrictTestCaptureRaw(t, anthropicStrictTool(t, prefix+test.definitions+`}`, false))
				if tool.Strict != nil {
					t.Fatalf("legacy tool carries strict: %v", *tool.Strict)
				}
				if string(tool.InputSchema) != want {
					t.Fatalf("input_schema\n got: %s\nwant: %s", tool.InputSchema, want)
				}
			})
		}
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

// Pi 4a6a58eb5 (#9557) at the HTTP boundary: the bytes the Anthropic provider sends keep root $defs after required, with
// the nested definition members in the authored order. Every Anthropic-compatible provider that builds its tools with
// anthConvertTools (OAuth identity, gateways through BaseURL, native tool_definition blocks) shares this legacy shape;
// Bedrock and Vertex Google send the tool parameters through their own converters and are not part of this change.
func TestAnthropicRequestBodyKeepsRootDefsInLegacyToolSchema(t *testing.T) {
	const parameters = `{"type":"object","properties":{"settings":{"$ref":"#/$defs/Settings"}},"required":["settings"],"anyOf":[{"type":"object"}],"$defs":{"Settings":{"type":"object","properties":{"verbose":{"type":"boolean"}}}}}`
	const want = `"input_schema":{"type":"object","properties":{"settings":{"$ref":"#/$defs/Settings"}},"required":["settings"],"$defs":{"Settings":{"type":"object","properties":{"verbose":{"type":"boolean"}}}}}`
	reply := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for _, test := range []struct {
		name   string
		config AnthropicConfig
	}{
		{"api key", AnthropicConfig{APIKey: "test-key"}},
		{"oauth token", AnthropicConfig{APIKey: "sk-ant-oat-test"}},
		{"strict tools supported", AnthropicConfig{APIKey: "test-key", Compat: &AnthropicMessagesCompat{SupportsStrictTools: new(true)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bodies := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				bodies <- string(body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, reply)
			}))
			defer server.Close()
			config := test.config
			config.Model, config.ProviderID, config.BaseURL = "claude-opus-4-8", "test-anthropic", server.URL
			provider := NewAnthropicProvider(config)
			defer provider.Close()
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{
				Messages: []Message{UserMessage{Content: UserText("Use the tool")}},
				Tools:    []ToolSchema{anthropicStrictTool(t, parameters, false)},
			}), StreamOptions{CacheRetention: CacheRetentionNone})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result == nil || result.StopReason != StopReasonStop {
				t.Fatalf("result = %#v", result)
			}
			if body := <-bodies; !strings.Contains(body, want+`}`) {
				t.Fatalf("request body lacks %s\nbody: %s", want, body)
			}
		})
	}

	t.Run("native tool_definition", func(t *testing.T) {
		tools, err := anthConvertTools([]ToolSchema{anthropicStrictTool(t, parameters, false)}, false, false, false, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(tools[0])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), want+`}`) {
			t.Fatalf("tool lacks %s\ntool: %s", want, encoded)
		}
	})

	t.Run("absent definitions are omitted", func(t *testing.T) {
		tools, err := anthConvertTools([]ToolSchema{anthropicStrictTool(t, `{"type":"object","properties":{"a":{"type":"string"}}}`, false)}, false, false, false, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(tools[0])
		if !strings.Contains(string(encoded), `"input_schema":{"type":"object","properties":{"a":{"type":"string"}},"required":[]}`) {
			t.Fatalf("tool = %s", encoded)
		}
	})
}

// Pi c815a3dfe (#12332): GitHub Copilot anthropic-messages models declare compat.supportsStrictTools=true,
// so constrained tools serialize with strict:true and full input_schema while ordinary tools stay non-strict.
// Providers whose anthropic-messages models do not set supportsStrictTools (for example OpenRouter and custom
// gateways) remain unchanged and omit strict.
func TestAnthropicCopilotStrictToolsSerializedRequest(t *testing.T) {
	const parameters = `{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"}},"required":["path"],"title":"ReadInput"}`
	const legacySchema = `{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"}},"required":["path"]}`
	const strictSchema = `{"type":"object","properties":{"path":{"type":"string"},"offset":{"anyOf":[{"type":"number"},{"type":"null"}]}},"required":["path","offset"],"title":"ReadInput","additionalProperties":false}`
	tools := []ToolSchema{
		anthropicStrictTool(t, parameters, false),
		anthropicStrictTool(t, parameters, true),
	}
	tools[0].Name = "ordinary"
	tools[1].Name = "constrained"

	type serializedTool struct {
		Name        string          `json:"name"`
		Strict      *bool           `json:"strict"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	capture := func(t *testing.T, model *Model) []serializedTool {
		t.Helper()
		payload := upstreamAnthropicParams(t, model, Context{
			Messages: []Message{UserMessage{Content: UserText("Use the tool")}},
			Tools:    tools,
		}, StreamOptions{CacheRetention: CacheRetentionNone})
		var got []serializedTool
		if err := json.Unmarshal(payload["tools"], &got); err != nil {
			t.Fatalf("unmarshal tools (%s): %v", payload["tools"], err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d tools, want 2: %s", len(got), payload["tools"])
		}
		return got
	}

	for _, tc := range []struct {
		provider, id string
		wantStrict   bool
	}{
		{"github-copilot", "claude-haiku-4.5", true},
		{"github-copilot", "claude-sonnet-5.5", true},
		{"anthropic", "claude-haiku-4-5", true},
		{"openrouter", "anthropic/claude-haiku-4.5", false},
	} {
		t.Run(tc.provider+"/"+tc.id, func(t *testing.T) {
			got := capture(t, upstreamCatalogModel(t, tc.provider, tc.id))
			if got[0].Name != "ordinary" || got[0].Strict != nil {
				t.Fatalf("ordinary tool = %+v, want strict absent", got[0])
			}
			assertShapeJSON(t, got[0].InputSchema, legacySchema)
			if got[1].Name != "constrained" {
				t.Fatalf("second tool = %+v, want constrained", got[1])
			}
			if tc.wantStrict {
				if got[1].Strict == nil || !*got[1].Strict {
					t.Fatalf("constrained tool strict = %v, want true", got[1].Strict)
				}
				assertShapeJSON(t, got[1].InputSchema, strictSchema)
			} else {
				if got[1].Strict != nil {
					t.Fatalf("constrained tool strict = %v, want absent", *got[1].Strict)
				}
				assertShapeJSON(t, got[1].InputSchema, legacySchema)
			}
		})
	}

	t.Run("custom gateway without flag omits strict", func(t *testing.T) {
		got := capture(t, customAdaptiveModel(nil))
		if got[0].Strict != nil || got[1].Strict != nil {
			t.Fatalf("custom gateway tools = %+v, want strict absent on both", got)
		}
		assertShapeJSON(t, got[0].InputSchema, legacySchema)
		assertShapeJSON(t, got[1].InputSchema, legacySchema)
	})
}

// Port of Pi main 4245b07b499c0dca35983aef72f03f4e09496b58 (#12212).
func TestAnthropicNonStrictToolPreservesDeclaredDialect(t *testing.T) {
	raw := `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"pair":{"type":"array","items":[{"type":"string"},{"type":"string"}],"minItems":2,"maxItems":2}},"required":["pair"]}`
	tool := anthropicStrictTestCapture(t, anthropicStrictTool(t, raw, false))
	if _, hasStrict := tool["strict"]; hasStrict {
		t.Fatalf("expected non-strict tool, got strict=%v", tool["strict"])
	}
	gotSchema, err := json.Marshal(tool["input_schema"])
	if err != nil {
		t.Fatalf("marshal input_schema: %v", err)
	}
	assertShapeJSON(t, gotSchema, `{"type":"object","properties":{"pair":{"type":"array","items":[{"type":"string"},{"type":"string"}],"minItems":2,"maxItems":2}},"required":["pair"],"$schema":"http://json-schema.org/draft-07/schema#"}`)
}
