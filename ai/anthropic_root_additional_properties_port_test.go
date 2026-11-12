package ai

import (
	"testing"
)

// Pi aa47cedce (#12100, post-v1.5.2, packages/ai/src/api/anthropic-messages.ts convertTools): a non-strict tool's input_schema keeps a root
// additionalProperties, whatever its value (false, true or a schema), after required and before $defs and definitions; root anyOf, oneOf, allOf
// and other root keywords stay out because Anthropic rejects root anyOf/oneOf/allOf with a 400. convertTools is reached only by the
// anthropic-messages API (the openai, google, mistral and bedrock paths send the whole parameters object), so the request test reads that API.
var anthropicRootAdditionalPropertiesCases = []struct{ name, parameters, want string }{
	{"false", `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false,"title":"LookupInput"}`,
		`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`},
	{"true", `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":true}`,
		`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":true}`},
	{"a schema", `{"type":"object","properties":{},"required":[],"additionalProperties":{"type":"string","description":"extra"}}`,
		`{"type":"object","properties":{},"required":[],"additionalProperties":{"type":"string","description":"extra"}}`},
	{"absent stays absent", `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"title":"LookupInput"}`,
		`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`},
	{"member order follows the literal, not the parameters", `{"$defs":{"S":{"type":"object"}},"additionalProperties":false,"required":["s"],"properties":{"s":{"$ref":"#/$defs/S"}},"type":"object"}`,
		`{"type":"object","properties":{"s":{"$ref":"#/$defs/S"}},"required":["s"],"additionalProperties":false,"$defs":{"S":{"type":"object"}}}`},
	{"with definitions and root anyOf out", `{"type":"object","properties":{},"anyOf":[{"type":"object"}],"oneOf":[{"type":"object"}],"allOf":[{"type":"object"}],"additionalProperties":false,"definitions":{"S":{"type":"object"}}}`,
		`{"type":"object","properties":{},"required":[],"additionalProperties":false,"definitions":{"S":{"type":"object"}}}`},
}

func TestAnthropicNonStrictInputSchemaKeepsRootAdditionalPropertiesPiAa47cedce(t *testing.T) {
	for _, test := range anthropicRootAdditionalPropertiesCases {
		t.Run("converter/"+test.name, func(t *testing.T) {
			tools, err := anthConvertTools([]ToolSchema{anthropicStrictTool(t, test.parameters, false)}, false, false, false, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := tools[0].wire()
			if err != nil {
				t.Fatal(err)
			}
			got := wire.InputSchema
			if string(got) != test.want {
				t.Fatalf("input_schema\n got: %s\nwant: %s", got, test.want)
			}
		})
		t.Run("request/"+test.name, func(t *testing.T) {
			captured := anthropicStrictTestCaptureRaw(t, anthropicStrictTool(t, test.parameters, false))
			if captured.Strict != nil {
				t.Fatalf("legacy tool carries strict: %v", *captured.Strict)
			}
			if string(captured.InputSchema) != test.want {
				t.Fatalf("input_schema\n got: %s\nwant: %s", captured.InputSchema, test.want)
			}
		})
	}
}

// The strict path already sent additionalProperties (Pi aa47cedce: "The strict path was already sending this keyword"); its wire is unchanged.
func TestAnthropicStrictInputSchemaStillSendsAdditionalPropertiesPiAa47cedce(t *testing.T) {
	captured := anthropicStrictTestCaptureRaw(t, anthropicStrictTool(t, `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`, true))
	assertShapeJSON(t, captured.InputSchema, `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)
}
