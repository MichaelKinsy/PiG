package ai

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func objectSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"payload": map[string]any{"type": "string"}},
		"required":             []any{"payload"},
		"additionalProperties": false,
	}
}

func sampleGrammarTool(cfg *ConstrainedSamplingConfig) ToolSchema {
	return ToolSchema{Name: "sample_tool", Description: "Sample tool", Parameters: objectSchema(), ConstrainedSampling: cfg}
}

// TestResolveJSONSchemaStrictSampling mirrors upstream resolveJsonSchemaStrictSampling.
func TestResolveJSONSchemaStrictSampling(t *testing.T) {
	// json_schema + supportsStrictMode → strict true.
	got, err := resolveJSONSchemaStrictSampling(sampleGrammarTool(&ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}), true)
	if err != nil || got == nil || *got != true {
		t.Fatalf("prefer+supported: got %v err %v, want &true", got, err)
	}
	// prefer + !supportsStrictMode → nil (fall back silently).
	got, err = resolveJSONSchemaStrictSampling(sampleGrammarTool(&ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}), false)
	if err != nil || got != nil {
		t.Fatalf("prefer+unsupported: got %v err %v, want nil", got, err)
	}
	// require + !supportsStrictMode → error.
	_, err = resolveJSONSchemaStrictSampling(sampleGrammarTool(&ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"}), false)
	if err == nil || !strings.Contains(err.Error(), `Tool "sample_tool" requires JSON-schema constrained sampling`) {
		t.Fatalf("require+unsupported err = %v, want the strict-required message", err)
	}
	// no config → nil.
	got, err = resolveJSONSchemaStrictSampling(sampleGrammarTool(nil), true)
	if err != nil || got != nil {
		t.Fatalf("no config: got %v err %v, want nil", got, err)
	}
}

// TestMakeStrictJSONSchema ports the nested optional-property case from
// upstream constrained-sampling.test.ts. The source schema must remain unchanged.
func TestMakeStrictJSONSchema(t *testing.T) {
	parameters := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":     map[string]any{"type": "string"},
			"offset":   map[string]any{"type": "number"},
			"metadata": map[string]any{"type": "object", "properties": map[string]any{"enabled": map[string]any{"type": "boolean"}}},
			"nullable": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}},
		},
		"required": []any{"path", "metadata"},
	}

	strict, err := makeStrictJSONSchema(parameters)
	if err != nil {
		t.Fatalf("makeStrictJSONSchema: %v", err)
	}
	if _, exists := parameters["additionalProperties"]; exists {
		t.Fatal("makeStrictJSONSchema mutated its input")
	}
	properties := strict["properties"].(map[string]any)
	offset := properties["offset"].(map[string]any)
	if _, ok := offset["anyOf"]; !ok {
		t.Fatalf("optional offset = %#v, want nullable anyOf", offset)
	}
	metadata := properties["metadata"].(map[string]any)
	if metadata["additionalProperties"] != false {
		t.Fatalf("nested additionalProperties = %#v, want false", metadata["additionalProperties"])
	}
	enabled := metadata["properties"].(map[string]any)["enabled"].(map[string]any)
	if _, ok := enabled["anyOf"]; !ok {
		t.Fatalf("optional nested property = %#v, want nullable anyOf", enabled)
	}
	required := toStringSlice(strict["required"])
	for _, name := range []string{"path", "offset", "metadata", "nullable"} {
		if !slices.Contains(required, name) {
			t.Fatalf("strict required = %v, missing %q", required, name)
		}
	}
}

func TestStrictJSONSchemaEmptyObjectsUseRequiredArray(t *testing.T) {
	rootCases := []map[string]any{
		{"type": "object"},
		{"type": "object", "properties": map[string]any{}},
		{"type": "object", "properties": map[string]any{}, "required": []string{}},
	}
	for index, parameters := range rootCases {
		strict, err := makeStrictJSONSchema(parameters)
		if err != nil {
			t.Fatalf("root case %d: %v", index, err)
		}
		assertSerializedRequiredArray(t, strict, "root")
	}

	strict, err := makeStrictJSONSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"nested": map[string]any{"type": "object"},
		},
		"required": []string{"nested"},
	})
	if err != nil {
		t.Fatalf("nested schema: %v", err)
	}
	nested := strict["properties"].(map[string]any)["nested"].(map[string]any)
	assertSerializedRequiredArray(t, nested, "nested")
}

func assertSerializedRequiredArray(t *testing.T, schema map[string]any, name string) {
	t.Helper()
	body, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal %s schema: %v", name, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode %s schema: %v", name, err)
	}
	if got := string(fields["required"]); got != "[]" {
		t.Fatalf("%s required = %s, want []; schema=%s", name, got, body)
	}
}

func TestStrictJSONSchemaRequiredIsCompleteAndUnique(t *testing.T) {
	strict, err := makeStrictJSONSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"b": map[string]any{"type": "string"},
			"a": map[string]any{"type": "number"},
		},
		"required": []any{"b", "b"},
	})
	if err != nil {
		t.Fatalf("makeStrictJSONSchema: %v", err)
	}
	if required := toStringSlice(strict["required"]); !reflect.DeepEqual(required, []string{"a", "b"}) {
		t.Fatalf("required = %v, want complete unique property names", required)
	}
}

func TestStrictJSONSchemaReportsUnsupportedKeywordBeforeRootType(t *testing.T) {
	_, err := makeStrictJSONSchema(map[string]any{"type": "array", "$ref": "https://example.test/schema"})
	if err == nil || !strings.Contains(err.Error(), "$ref schemas are unsupported") {
		t.Fatalf("error = %v, want unsupported $ref", err)
	}
}

// TestStrictJSONSchemaUnsupported ports upstream's fallback-versus-require cases.
func TestStrictJSONSchemaUnsupported(t *testing.T) {
	cases := []struct {
		name       string
		parameters map[string]any
		wantError  string
	}{
		{
			name: "schema additionalProperties",
			parameters: map[string]any{"type": "object", "properties": map[string]any{
				"metadata": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			}},
			wantError: "additionalProperties is unsupported",
		},
		{name: "allOf", parameters: map[string]any{"type": "object", "allOf": []any{}}, wantError: "allOf schemas are unsupported"},
		{
			name: "structured union",
			parameters: map[string]any{"type": "object", "properties": map[string]any{
				"value": map[string]any{"anyOf": []any{map[string]any{"type": "object", "properties": map[string]any{}}, map[string]any{"type": "null"}}},
			}},
			wantError: "object and array unions are unsupported",
		},
		{
			name: "ref",
			parameters: map[string]any{"type": "object", "properties": map[string]any{
				"child": map[string]any{"$ref": "https://example.com/child.json"},
			}, "required": []any{"child"}},
			wantError: "$ref schemas are unsupported",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := makeStrictJSONSchema(test.parameters); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("makeStrictJSONSchema error = %v, want %q", err, test.wantError)
			}
			tool := ToolSchema{Name: "sample_tool", Parameters: test.parameters, ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}}
			strict, err := resolveJSONSchemaStrictSampling(tool, true)
			if err != nil || strict != nil {
				t.Fatalf("prefer unsupported schema = %v, %v; want nil, nil", strict, err)
			}
			tool.ConstrainedSampling.Strict = "require"
			if _, err := resolveJSONSchemaStrictSampling(tool, true); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("require unsupported schema error = %v, want %q", err, test.wantError)
			}
		})
	}
}

// TestResolveGrammarConstrainedSampling mirrors upstream resolveGrammarConstrainedSampling.
func TestResolveGrammarConstrainedSampling(t *testing.T) {
	lark := sampleGrammarTool(&ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{GrammarFormatOpenAILark: "start: /[a-z]+/"}})
	g, err := resolveGrammarConstrainedSampling(lark, true)
	if err != nil || g == nil || g.Format != "lark" || g.Definition != "start: /[a-z]+/" || g.InputProperty != "payload" {
		t.Fatalf("lark grammar = %+v err %v", g, err)
	}
	// regex-only variant.
	rx := sampleGrammarTool(&ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{GrammarFormatOpenAIRegex: "[0-9]+"}})
	g, err = resolveGrammarConstrainedSampling(rx, true)
	if err != nil || g == nil || g.Format != "regex" || g.Definition != "[0-9]+" {
		t.Fatalf("regex grammar = %+v err %v", g, err)
	}
	// lark preferred over regex when both present.
	both := sampleGrammarTool(&ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{GrammarFormatOpenAILark: "L", GrammarFormatOpenAIRegex: "R"}})
	g, _ = resolveGrammarConstrainedSampling(both, true)
	if g == nil || g.Format != "lark" || g.Definition != "L" {
		t.Fatalf("lark+regex = %+v, want lark preferred", g)
	}
	// unsupported → nil.
	g, err = resolveGrammarConstrainedSampling(lark, false)
	if err != nil || g != nil {
		t.Fatalf("unsupported grammar: got %+v err %v, want nil", g, err)
	}
	// no variant → error.
	_, err = resolveGrammarConstrainedSampling(sampleGrammarTool(&ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{}}), true)
	if err == nil || !strings.Contains(err.Error(), "no supported grammar variant was provided") {
		t.Fatalf("empty variants err = %v", err)
	}
}

// TestInferGrammarInputProperty mirrors upstream inferGrammarInputProperty guards.
func TestInferGrammarInputProperty(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
		wantOK bool
		errsub string
	}{
		{"valid", objectSchema(), true, ""},
		{"not object", map[string]any{"type": "array"}, false, "requires an object parameter schema"},
		{"two required", map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []any{"a", "b"}}, false, "exactly one required string property"},
		{"missing property entry", map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{"payload"}}, false, "requires a properties entry for payload"},
		{"non-string property", map[string]any{"type": "object", "properties": map[string]any{"payload": map[string]any{"type": "number"}}, "required": []any{"payload"}}, false, "must have type string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := inferGrammarInputProperty(ToolSchema{Name: "sample_tool", Parameters: c.params})
			if c.wantOK {
				if err != nil || got != "payload" {
					t.Fatalf("got %q err %v, want payload", got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.errsub) {
				t.Fatalf("err = %v, want substring %q", err, c.errsub)
			}
		})
	}
}

// TestGetGrammarToolInput mirrors upstream getGrammarToolInput.
func TestGetGrammarToolInput(t *testing.T) {
	got, err := getGrammarToolInput("sample_tool", map[string]any{"payload": "abc"}, "payload")
	if err != nil || got != "abc" {
		t.Fatalf("got %q err %v", got, err)
	}
	for _, bad := range []map[string]any{{}, {"payload": 42}} {
		if _, err := getGrammarToolInput("sample_tool", bad, "payload"); err == nil ||
			!strings.Contains(err.Error(), `Grammar tool call "sample_tool" requires argument "payload" to be a string`) {
			t.Fatalf("bad args %v err = %v, want the string-required message", bad, err)
		}
	}
}

// TestAppendGrammarToolInputJSONDelta pins the append-only buffer against the
// upstream oracle: reconstructed deltas parse back to the input, an idempotent
// close yields no delta, and a post-close change errors.
func TestAppendGrammarToolInputJSONDelta(t *testing.T) {
	buf := &grammarToolInputJSONBuffer{}
	first, ok1, err1 := appendGrammarToolInputJSONDelta(buf, "payload", `a"`, false)
	second, ok2, err2 := appendGrammarToolInputJSONDelta(buf, "payload", "a\"\nb", true)
	if err1 != nil || err2 != nil || !ok1 || !ok2 {
		t.Fatalf("append errors: %v %v (ok %v %v)", err1, err2, ok1, ok2)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(first+second), &out); err != nil {
		t.Fatalf("reconstructed JSON invalid: %q: %v", first+second, err)
	}
	if out["payload"] != "a\"\nb" {
		t.Fatalf("reconstructed payload = %q, want %q", out["payload"], "a\"\nb")
	}
	// Idempotent close → no delta.
	if _, ok, err := appendGrammarToolInputJSONDelta(buf, "payload", "a\"\nb", true); ok || err != nil {
		t.Fatalf("idempotent close: ok %v err %v, want no delta", ok, err)
	}
	// Change after close → error.
	if _, _, err := appendGrammarToolInputJSONDelta(buf, "payload", "changed", true); err == nil ||
		!strings.Contains(err.Error(), `changed after it was closed`) {
		t.Fatalf("post-close change err = %v", err)
	}
}

// TestAppendGrammarToolInputJSONDelta_NonMonotonic pins the monotonicity guard.
func TestAppendGrammarToolInputJSONDelta_NonMonotonic(t *testing.T) {
	buf := &grammarToolInputJSONBuffer{}
	if _, _, err := appendGrammarToolInputJSONDelta(buf, "payload", "hello", false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := appendGrammarToolInputJSONDelta(buf, "payload", "help", false); err == nil ||
		!strings.Contains(err.Error(), "changed non-monotonically") {
		t.Fatalf("non-monotonic err = %v", err)
	}
}

// TestJSONStringJS pins byte-faithful JS JSON.stringify escaping (no HTML escaping).
func TestJSONStringJS(t *testing.T) {
	if got := jsonStringJS("a<b>&c"); got != `"a<b>&c"` {
		t.Fatalf("jsonStringJS did HTML-escape: %q, want unescaped", got)
	}
	if got := jsonStringJS("x\"y"); got != `"x\"y"` {
		t.Fatalf("quote escape = %q", got)
	}
}

// TestCreateGrammarToolInputProperties mirrors upstream createGrammarToolInputProperties.
func TestCreateGrammarToolInputProperties(t *testing.T) {
	tools := []ToolSchema{
		sampleGrammarTool(&ConstrainedSamplingConfig{Type: "grammar", Variants: map[string]string{GrammarFormatOpenAILark: "L"}}),
		{Name: "plain", Parameters: map[string]any{"type": "object"}},
	}
	props, err := createGrammarToolInputProperties(tools, true)
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if props["sample_tool"] != "payload" {
		t.Fatalf("props = %v, want sample_tool→payload only", props)
	}
	if _, ok := props["plain"]; ok {
		t.Fatalf("plain tool should not be a grammar tool")
	}
}
