package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGoogleSharedConvertToolsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		useParameters     bool
	}{
		// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:18
		{"strips JSON Schema meta keys from parameters when useParameters=true", `{"$schema":"http://json-schema.org/draft-07/schema#","$id":"urn:bash-tool","$comment":"A bash tool for demonstration","$defs":{"commandDef":{"type":"string"}},"definitions":{"legacyDef":{"type":"number"}},"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`, `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`, true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:56
		{"recursively strips nested JSON Schema meta keys", `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"deep":{"$schema":"http://json-schema.org/draft-07/schema#","$id":"urn:nested","type":"string"}}}`, `{"type":"object","properties":{"deep":{"type":"string"}}}`, true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:85
		{"preserves $ref while stripping meta keys", `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"refProp":{"$ref":"#/$defs/someDef","type":"string"}}}`, `{"type":"object","properties":{"refProp":{"$ref":"#/$defs/someDef","type":"string"}}}`, true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:114
		{"does not mutate the original Tool.parameters object", `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`, `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`, true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:137
		{"preserves $schema in parametersJsonSchema when useParameters=false", `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`, `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`, false},
		// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:163
		{"handles tools without $schema gracefully", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`, `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var parameters map[string]any
			if err := json.Unmarshal([]byte(tc.input), &parameters); err != nil {
				t.Fatal(err)
			}
			tools, _, err := geminiConvertTools([]ToolSchema{{Name: "test_tool", Description: "A test tool", Parameters: parameters}}, tc.useParameters, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
				t.Fatalf("tools=%#v", tools)
			}
			decl := tools[0].FunctionDeclarations[0]
			var got map[string]any
			if tc.useParameters {
				got = decl.Parameters
				if decl.ParametersJSONSchema != nil {
					t.Fatal("unexpected parametersJsonSchema")
				}
			} else {
				got = decl.ParametersJSONSchema
				if decl.Parameters != nil {
					t.Fatal("unexpected parameters")
				}
			}
			assertCompletionsJSON(t, got, tc.want)
			assertCompletionsJSON(t, parameters, tc.input)
		})
	}
	// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:187
	t.Run("uses validated function calling for strict tools on Gemini 3", func(t *testing.T) {
		tool := ToolSchema{Name: "test_tool", Description: "A test tool", Parameters: JsonObject{"type": "object", "properties": JsonObject{}}, ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"}}
		if !supportsGoogleStrictToolSampling("gemini-3.1-pro-preview") || supportsGoogleStrictToolSampling("gemini-2.5-pro") {
			t.Fatal("strict model support mismatch")
		}
		_, strict, err := geminiConvertTools([]ToolSchema{tool}, false, true)
		if err != nil || !strict {
			t.Fatalf("strict=%v err=%v", strict, err)
		}
		_, _, err = geminiConvertTools([]ToolSchema{tool}, false, false)
		if err == nil || !strings.Contains(err.Error(), `Tool "test_tool" requires JSON-schema constrained sampling`) {
			t.Fatalf("required strict error=%v", err)
		}
		body := captureShapeRequest(t, func(url string) Provider {
			return NewGoogleProvider(GoogleConfig{Model: "gemini-3.1-pro-preview", APIKey: "test", BaseURL: url})
		}, []Message{SystemMessage{ToolsAdded: []ToolSchema{tool}}, UserMessage{Content: UserText("Hello")}}, StreamOptions{}, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
		assertShapeJSON(t, body["toolConfig"], `{"functionCallingConfig":{"mode":"VALIDATED"}}`)
	})
	// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:199
	t.Run("returns undefined for empty tool list", func(t *testing.T) {
		for _, useParameters := range []bool{false, true} {
			tools, _, err := geminiConvertTools([]ToolSchema{}, useParameters, true)
			if err != nil || tools != nil {
				t.Fatalf("tools=%#v err=%v", tools, err)
			}
		}
	})
}
