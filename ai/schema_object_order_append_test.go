package ai

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// referenceMarshalSchemaWithOrder is the buffer-per-level walk appendSchemaWithOrder replaced; it is the oracle for the
// bytes.
func referenceMarshalSchemaWithOrder(value any, order schemaObjectOrder, path string) ([]byte, error) {
	switch value := value.(type) {
	case map[string]any:
		if value == nil {
			return []byte("null"), nil
		}
		var out bytes.Buffer
		out.WriteByte('{')
		for i, key := range orderedSchemaKeys(value, order, path) {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(jsonStringJS(key))
			out.WriteByte(':')
			encoded, err := referenceMarshalSchemaWithOrder(value[key], order, schemaPath(path, key))
			if err != nil {
				return nil, err
			}
			out.Write(encoded)
		}
		out.WriteByte('}')
		return out.Bytes(), nil
	case []any:
		if value == nil {
			return []byte("null"), nil
		}
		var out bytes.Buffer
		out.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				out.WriteByte(',')
			}
			encoded, err := referenceMarshalSchemaWithOrder(item, order, schemaPath(path, strconv.Itoa(i)))
			if err != nil {
				return nil, err
			}
			out.Write(encoded)
		}
		out.WriteByte(']')
		return out.Bytes(), nil
	default:
		return jsstring.MarshalJSON(value)
	}
}

// referenceToolSchemaMarshal is ToolSchema.MarshalJSON before the checked walk: validate with encoding/json, then walk.
func referenceToolSchemaMarshal(tool ToolSchema) ([]byte, error) {
	if _, err := json.Marshal(tool.Parameters); err != nil {
		return nil, err
	}
	parameters, err := referenceMarshalSchemaWithOrder(tool.Parameters, tool.parameterOrder, "")
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Name             string          `json:"name"`
		Description      string          `json:"description"`
		Parameters       json.RawMessage `json:"parameters"`
		PromptGuidelines []string        `json:"promptGuidelines,omitempty"`
	}{tool.Name, tool.Description, parameters, tool.PromptGuidelines})
}

func TestOrderedToolSchemaMarshalMatchesTheBufferedWalk(t *testing.T) {
	schemas := []string{
		gateOrderedSchema,
		`{"type":"object","properties":{"":{"type":"string"},"a/b~c":{"type":"object","properties":{"z":{},"y":{}}},"10":{"enum":["<&>","\u2028","é","\"q\"","back\\slash","tab\t","\u0001"]},"2":{"default":null},"x":{"const":true,"minimum":-0.5,"maximum":1e21,"multipleOf":1e-7}},"required":["","10","2"]}`,
		`{"anyOf":[{"type":"array","items":[{"type":"object","properties":{"b":{"type":"number"},"a":{"type":"string"}}},[]]},{"type":"null"}]}`,
		`{"b":{"y":1,"x":[2,{}]},"a":[]}`,
	}
	for _, source := range schemas {
		var tool ToolSchema
		if err := json.Unmarshal([]byte(`{"name":"n<1>","description":"d & \u2028","parameters":`+source+`}`), &tool); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		if tool.parameterOrder == nil {
			t.Fatalf("%s: no key order", source)
		}
		want, wantErr := referenceToolSchemaMarshal(tool)
		got, gotErr := json.Marshal(tool)
		if wantErr != nil || gotErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s:\n got %s (%v)\nwant %s (%v)", source, got, gotErr, want, wantErr)
		}
	}
}

// The checked walk hands every value encoding/json might reject, and every cycle, to the validated walk, so the errors are
// encoding/json's.
func TestOrderedToolSchemaMarshalReportsEncodingJSONErrors(t *testing.T) {
	newTool := func(parameters map[string]any) ToolSchema {
		var tool ToolSchema
		if err := json.Unmarshal([]byte(`{"name":"t","parameters":{"type":"object","properties":{"a":{"type":"string"}}}}`), &tool); err != nil {
			t.Fatal(err)
		}
		if tool.parameterOrder == nil {
			t.Fatal("the tool must carry a recorded key order, or MarshalJSON never takes the ordered walk")
		}
		tool.Parameters = parameters
		return tool
	}
	cycle := map[string]any{"type": "object"}
	cycle["self"] = cycle
	// encoding/json starts its own cycle detection after 1000 levels; a chain past that is not a cycle.
	deep := map[string]any{}
	for range 3000 {
		deep = map[string]any{"items": deep}
	}
	sliceCycle := []any{"x", nil}
	sliceCycle[1] = sliceCycle
	prefixNotCycle := []any{"x", nil}
	prefixNotCycle[1] = prefixNotCycle[:1]
	sibling := map[string]any{"type": "string"}
	cases := map[string]map[string]any{
		"nan":         {"minimum": math.NaN()},
		"inf":         {"items": []any{math.Inf(1)}},
		"func":        {"default": func() {}},
		"cycle":       cycle,
		"deep":        deep,
		"slice cycle": {"items": sliceCycle},
		"prefix":      {"items": prefixNotCycle},
		"shared":      {"a": sibling, "b": sibling, "c": []any{sibling, sibling}},
		"float32":     {"minimum": float32(1.5)},
		"json number": {"minimum": json.Number("12")},
	}
	for name, parameters := range cases {
		tool := newTool(parameters)
		want, wantErr := referenceToolSchemaMarshal(tool)
		got, gotErr := tool.MarshalJSON()
		if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) || !bytes.Equal(got, want) {
			t.Errorf("%s: got %.80s (%v), want %.80s (%v)", name, got, gotErr, want, wantErr)
		}
	}
}

const gateOrderedSchema = `{"type":"object","required":["path","edits"],"properties":{"path":{"type":"string","description":"Path"},"edits":{"type":"array","items":{"type":"object","required":["oldText","newText"],"properties":{"oldText":{"type":"string"},"newText":{"type":"string"}},"additionalProperties":false}}},"additionalProperties":false}`
