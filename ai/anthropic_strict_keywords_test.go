package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAnthropicStrictUnsupportedKeyword covers isAnthropicStrictUnsupportedKeyword in
// .upstream/v0.99.2/packages/ai/src/api/anthropic-messages.ts:1548-1580.
func TestAnthropicStrictUnsupportedKeyword(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value any
		want  bool
	}{
		{"minimum", float64(1), true},
		{"maximum", float64(300000), true},
		{"exclusiveMinimum", float64(0), true},
		{"exclusiveMaximum", float64(9), true},
		{"multipleOf", float64(2), true},
		{"maxItems", float64(3), true},
		{"uniqueItems", true, true},
		{"minContains", float64(1), true},
		{"maxContains", float64(2), true},
		{"minProperties", float64(1), true},
		{"maxProperties", float64(2), true},
		{"minItems", float64(0), false},
		{"minItems", float64(1), false},
		{"minItems", float64(2), true},
		{"minItems", "1", true},
		{"format", "date-time", false},
		{"format", "time", false},
		{"format", "date", false},
		{"format", "duration", false},
		{"format", "email", false},
		{"format", "hostname", false},
		{"format", "uri", false},
		{"format", "ipv4", false},
		{"format", "ipv6", false},
		{"format", "uuid", false},
		{"format", "regex", true},
		{"format", "iri", true},
		{"format", float64(1), true},
		{"minLength", float64(1), false},
		{"maxLength", float64(10), false},
		{"pattern", "^a$", false},
		{"description", "minimum", false},
	} {
		if got := anthropicStrictUnsupportedKeyword(tc.key, tc.value); got != tc.want {
			t.Errorf("anthropicStrictUnsupportedKeyword(%q, %v) = %v, want %v", tc.key, tc.value, got, tc.want)
		}
	}
}

// TestStrictSamplingProviderKeywordCheck covers constrained-sampling.ts:12-14, 56-71 and 221-246: a provider
// keyword check runs on every schema node, including anyOf variants, array items and object properties, and
// rejects the schema like any other unsupported strict construct.
func TestStrictSamplingProviderKeywordCheck(t *testing.T) {
	rejectMinimum := func(key string, _ any) bool { return key == "minimum" }
	tool := func(t *testing.T, parameters, strict string) ToolSchema {
		t.Helper()
		var decoded ToolSchema
		document := `{"name":"lookup","description":"d","parameters":` + parameters + `,"constrainedSampling":{"type":"json_schema","strict":"` + strict + `"}}`
		if err := json.Unmarshal([]byte(document), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	for name, parameters := range map[string]string{
		"property":      `{"type":"object","properties":{"n":{"type":"integer","minimum":1}},"required":["n"]}`,
		"nested object": `{"type":"object","properties":{"o":{"type":"object","properties":{"n":{"type":"number","minimum":0}},"required":["n"]}},"required":["o"]}`,
		"array items":   `{"type":"object","properties":{"a":{"type":"array","items":{"type":"number","minimum":0}}},"required":["a"]}`,
		"anyOf variant": `{"type":"object","properties":{"u":{"anyOf":[{"type":"number","minimum":0},{"type":"null"}]}},"required":["u"]}`,
		"root":          `{"type":"object","minimum":1,"properties":{}}`,
	} {
		t.Run(name+" prefer falls back to non-strict", func(t *testing.T) {
			strict, err := resolveJSONSchemaStrictSampling(tool(t, parameters, "prefer"), true, rejectMinimum)
			if err != nil || strict != nil {
				t.Fatalf("strict = %v, err = %v; want nil, nil", strict, err)
			}
		})
		t.Run(name+" require fails", func(t *testing.T) {
			_, err := resolveJSONSchemaStrictSampling(tool(t, parameters, "require"), true, rejectMinimum)
			if err == nil || !strings.Contains(err.Error(), "minimum: ") || !strings.Contains(err.Error(), "is unsupported") {
				t.Fatalf("err = %v; want a minimum keyword rejection", err)
			}
		})
	}
	t.Run("the check does not see property names", func(t *testing.T) {
		parameters := `{"type":"object","properties":{"minimum":{"type":"integer"}},"required":["minimum"]}`
		strict, err := resolveJSONSchemaStrictSampling(tool(t, parameters, "prefer"), true, rejectMinimum)
		if err != nil || strict == nil || !*strict {
			t.Fatalf("strict = %v, err = %v; want true", strict, err)
		}
	})
	t.Run("a nil check keeps every keyword", func(t *testing.T) {
		parameters := `{"type":"object","properties":{"n":{"type":"integer","minimum":1}},"required":["n"]}`
		strict, err := resolveJSONSchemaStrictSampling(tool(t, parameters, "prefer"), true, nil)
		if err != nil || strict == nil || !*strict {
			t.Fatalf("strict = %v, err = %v; want true", strict, err)
		}
	})
}
