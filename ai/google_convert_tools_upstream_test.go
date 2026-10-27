package ai

import (
	"bytes"
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
			tools, err := geminiConvertTools([]ToolSchema{{Name: "test_tool", Description: "A test tool", Parameters: parameters}}, tc.useParameters, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
				t.Fatalf("tools=%#v", tools)
			}
			decl := tools[0].FunctionDeclarations[0]
			// convertTools declares {name, description, parameters|parametersJsonSchema} (google-shared.ts:388-397).
			schemaKey := "parametersJsonSchema"
			if tc.useParameters {
				schemaKey = "parameters"
			}
			if len(decl) != 3 || decl[0] != (geminiDeclMember{"name", "test_tool"}) || decl[1] != (geminiDeclMember{"description", "A test tool"}) || decl[2].Key != schemaKey {
				t.Fatalf("declaration = %#v", decl)
			}
			schema, ok := decl[2].Value.(jsonSchemaToolParameters)
			if !ok {
				t.Fatalf("schema = %#v", decl[2].Value)
			}
			encoded, err := json.Marshal(decl)
			if err != nil {
				t.Fatal(err)
			}
			var members map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &members); err != nil {
				t.Fatal(err)
			}
			if _, present := members[schemaKey]; !present || len(members) != 3 || !bytes.HasPrefix(encoded, []byte(`{"name":"test_tool","description":"A test tool","`+schemaKey+`":`)) {
				t.Fatalf("declaration JSON = %s", encoded)
			}
			assertCompletionsJSON(t, schema.Schema, tc.want)
			assertCompletionsJSON(t, parameters, tc.input)
		})
	}
	// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:187
	t.Run("uses validated function calling for strict tools on Gemini 3", func(t *testing.T) {
		tool := ToolSchema{Name: "test_tool", Description: "A test tool", Parameters: JsonObject{"type": "object", "properties": JsonObject{}}, ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"}}
		if !supportsGoogleStrictToolSampling("gemini-3.1-pro-preview") || supportsGoogleStrictToolSampling("gemini-2.5-pro") {
			t.Fatal("strict model support mismatch")
		}
		if mode, err := resolveGoogleFunctionCallingMode([]ToolSchema{tool}, "", true); err != nil || mode != "VALIDATED" {
			t.Fatalf("mode=%q err=%v", mode, err)
		}
		_, err := resolveGoogleFunctionCallingMode([]ToolSchema{tool}, "", false)
		if err == nil || !strings.Contains(err.Error(), `Tool "test_tool" requires JSON-schema constrained sampling`) {
			t.Fatalf("required strict error=%v", err)
		}
		if _, err := geminiConvertTools([]ToolSchema{tool}, false, false); err == nil || !strings.Contains(err.Error(), `Tool "test_tool" requires JSON-schema constrained sampling`) {
			t.Fatalf("convertTools required strict error=%v", err)
		}
		body := captureShapeRequest(t, func(url string) Provider {
			return NewGoogleProvider(GoogleConfig{Model: "gemini-3.1-pro-preview", APIKey: "test", BaseURL: url})
		}, []Message{SystemMessage{ToolsAdded: []ToolSchema{tool}}, UserMessage{Content: UserText("Hello")}}, StreamOptions{}, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
		assertShapeJSON(t, body["toolConfig"], `{"functionCallingConfig":{"mode":"VALIDATED"}}`)
	})
	// .upstream/v0.87.1/packages/ai/test/google-shared-convert-tools.test.ts:199
	t.Run("returns undefined for empty tool list", func(t *testing.T) {
		for _, useParameters := range []bool{false, true} {
			tools, err := geminiConvertTools([]ToolSchema{}, useParameters, true)
			if err != nil || tools != nil {
				t.Fatalf("tools=%#v err=%v", tools, err)
			}
		}
	})
}

// convertedGoogleParameters is the parametersJsonSchema text geminiConvertTools writes for the authored parameters text, and the authored
// tool, which the conversion must leave as it was.
func convertedGoogleParameters(t *testing.T, parameters string) (string, ToolSchema) {
	t.Helper()
	var tool ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"test_tool","description":"A test tool","parameters":`+parameters+`}`), &tool); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(tool.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := geminiConvertTools([]ToolSchema{tool}, false, true)
	if err != nil || len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools=%#v err=%v", tools, err)
	}
	decl := tools[0].FunctionDeclarations[0]
	if decl[2].Key != "parametersJsonSchema" {
		t.Fatalf("declaration = %#v", decl)
	}
	encoded, err := json.Marshal(decl[2].Value)
	if err != nil {
		t.Fatal(err)
	}
	if after, err := json.Marshal(tool.Parameters); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the authored parameters changed: %s -> %s (%v)", before, after, err)
	}
	return string(encoded), tool
}

// Regression coverage for #11308 (Pi 7314819f0, a7b94a03f): .upstream/current/packages/ai/test/google-shared-convert-tools.test.ts
// "google-shared convertTools const handling".
func TestGoogleSharedConvertToolsConstHandlingUpstream(t *testing.T) {
	const literal = `{"const":"literal-data","properties":{"nested":{"const":1}}}`
	for _, key := range []string{"default", "examples", "enum", "const", "x-custom"} {
		t.Run("preserves literal data under "+key, func(t *testing.T) {
			value := literal
			if key == "enum" || key == "examples" {
				value = "[" + literal + "]"
			}
			parameters := `{"type":"object","properties":{"value":{"type":"object","` + key + `":` + value + `}}}`
			if got, _ := convertedGoogleParameters(t, parameters); got != parameters {
				t.Fatalf("parametersJsonSchema = %s, want %s", got, parameters)
			}
		})
	}
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies"} {
		t.Run("preserves names while rewriting schemas in "+key, func(t *testing.T) {
			parameters := `{"type":"object","` + key + `":{"__proto__":{"const":"x"},"const":{"const":"y"},"properties":{"const":"z"}}}`
			want := `{"type":"object","` + key + `":{"__proto__":{"enum":["x"]},"const":{"enum":["y"]},"properties":{"enum":["z"]}}}`
			if got, _ := convertedGoogleParameters(t, parameters); got != want {
				t.Fatalf("parametersJsonSchema = %s, want %s", got, want)
			}
		})
	}
	t.Run("rewrites only schema-bearing keywords", func(t *testing.T) {
		parameters := `{"type":"object","additionalProperties":{"const":"x"},"unevaluatedProperties":false,"propertyNames":{"const":"name"},` +
			`"allOf":[{"if":{"const":"x"},"then":{"const":"y"},"else":{"not":{"const":"z"}}}],` +
			`"properties":{"list":{"type":"array","items":[{"const":1}],"prefixItems":[{"const":2}],"additionalItems":{"const":3},"contains":{"const":4},"unevaluatedItems":{"const":5}}},` +
			`"dependencies":{"list":["other"]}}`
		want := `{"type":"object","additionalProperties":{"enum":["x"]},"unevaluatedProperties":false,"propertyNames":{"enum":["name"]},` +
			`"allOf":[{"if":{"enum":["x"]},"then":{"enum":["y"]},"else":{"not":{"enum":["z"]}}}],` +
			`"properties":{"list":{"type":"array","items":[{"enum":[1]}],"prefixItems":[{"enum":[2]}],"additionalItems":{"enum":[3]},"contains":{"enum":[4]},"unevaluatedItems":{"enum":[5]}}},` +
			`"dependencies":{"list":["other"]}}`
		if got, _ := convertedGoogleParameters(t, parameters); got != want {
			t.Fatalf("parametersJsonSchema = %s, want %s", got, want)
		}
	})
	t.Run("rewrites string and number const values to single-value enums in parametersJsonSchema", func(t *testing.T) {
		parameters := `{"type":"object","properties":{"origin":{"type":"string","const":"zq-src-7"},` +
			`"mode":{"anyOf":[{"type":"string","const":"a"},{"type":"string","const":"b"}]},"level":{"oneOf":[{"const":1},{"const":2}]},` +
			`"flag":{"type":"boolean","const":true},"list":{"type":"array","items":{"const":"x"}},"const":{"type":"string"}},"required":["origin"]}`
		want := `{"type":"object","properties":{"origin":{"type":"string","enum":["zq-src-7"]},` +
			`"mode":{"anyOf":[{"type":"string","enum":["a"]},{"type":"string","enum":["b"]}]},"level":{"oneOf":[{"enum":[1]},{"enum":[2]}]},` +
			`"flag":{"type":"boolean","const":true},"list":{"type":"array","items":{"enum":["x"]}},"const":{"type":"string"}},"required":["origin"]}`
		got, tool := convertedGoogleParameters(t, parameters)
		if got != want {
			t.Fatalf("parametersJsonSchema = %s, want %s", got, want)
		}
		origin := tool.Parameters["properties"].(map[string]any)["origin"].(map[string]any)
		if origin["const"] != "zq-src-7" || origin["enum"] != nil {
			t.Fatalf("the authored origin schema changed: %v", origin)
		}
	})
	t.Run("keeps const in the legacy parameters field", func(t *testing.T) {
		var tool ToolSchema
		if err := json.Unmarshal([]byte(`{"name":"test_tool","description":"d","parameters":{"type":"object","properties":{"origin":{"type":"string","const":"x"}}}}`), &tool); err != nil {
			t.Fatal(err)
		}
		tools, err := geminiConvertTools([]ToolSchema{tool}, true, true)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(tools[0].FunctionDeclarations[0][2].Value)
		if err != nil || !strings.Contains(string(encoded), `"const":"x"`) || strings.Contains(string(encoded), `"enum"`) {
			t.Fatalf("parameters = %s (%v), want const kept and no enum", encoded, err)
		}
	})
}
