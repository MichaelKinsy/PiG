package sdk

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestTypeEngineWireRoundTrip reads the wire text TypeBox's own values have in test/parity/sdk-vectors/pi-ai/Type.json (the "wire" of every case the host's type engine
// answers) through the SDK's reader, and checks the schema it makes is Pi's (the JSON text and the hidden members) and that the SDK's writer reads back as the same schema. The
// reader and writer are what every engine member goes through; the host's side is checked against the same vectors in internal/typebox/engine.
func TestTypeEngineWireRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "parity", "sdk-vectors", "pi-ai", "Type.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name   string
			Result json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, c := range file.Cases {
		var result struct {
			JSON            *string        `json:"json"`
			StringifyThrows *string        `json:"stringifyThrows"`
			Hidden          map[string]any `json:"hidden"`
			Wire            string         `json:"wire"`
		}
		if len(c.Result) == 0 || c.Result[0] != '{' || json.Unmarshal(c.Result, &result) != nil || result.Wire == "" || (result.JSON == nil && result.StringifyThrows == nil) {
			continue
		}
		reader := &wireReader{text: result.Wire}
		value := reader.value()
		schema, isSchema := value.(*TSchema)
		if !isSchema {
			if plain, isObject := value.(*OrderedObject); isObject {
				schema, isSchema = &TSchema{object: plain, hidden: map[string]any{}}, true
			}
		}
		if !isSchema {
			if _, isList := value.([]any); isList {
				continue // a schema made of a JavaScript array: its wire form is the plain list
			}
			t.Errorf("%s: the wire value is a %T", c.Name, value)
			continue
		}
		if len(result.Hidden) == 0 {
			// A schema value always carries its hidden members; a plain object (Module's answer) has none.
			if len(schema.hidden) != 0 {
				t.Errorf("%s: hidden %v, Pi has none", c.Name, schema.hidden)
			}
		}
		text, err := schema.MarshalJSON()
		if result.StringifyThrows != nil {
			// The value holds a bigint: reading it is fine, writing it as JSON throws Pi's message.
			if err == nil || !strings.Contains(err.Error(), *result.StringifyThrows) {
				t.Errorf("%s: MarshalJSON gave %q, %v; Pi's JSON.stringify throws %q", c.Name, text, err, *result.StringifyThrows)
				continue
			}
		} else if err != nil || string(text) != *result.JSON {
			t.Errorf("%s:\n got  %s (%v)\n Pi   %s", c.Name, text, err, *result.JSON)
			continue
		}
		for name, want := range result.Hidden {
			if name == "~refine" {
				continue // functions have no JSON form: the vector records how many there are
			}
			if got, ok := schema.hidden[name]; !ok || !reflect.DeepEqual(got, want) {
				t.Errorf("%s: hidden %s = %v (%v), Pi %v", c.Name, name, got, ok, want)
			}
		}
		var written bytes.Buffer
		wireEncode(&written, schema, new([]Refinement))
		again := (&wireReader{text: written.String()}).value()
		againSchema, _ := again.(*TSchema)
		if againSchema == nil && len(schema.hidden) == 0 {
			if plain, _ := again.(*OrderedObject); plain != nil {
				againSchema = &TSchema{object: plain, hidden: map[string]any{}}
			}
		}
		if againSchema == nil || !reflect.DeepEqual(againSchema, schema) {
			t.Errorf("%s: the schema the writer's text reads back as differs\n wrote %s", c.Name, written.String())
		}
		checked++
	}
	if checked < 300 {
		t.Fatalf("only %d cases carry wire text", checked)
	}
}
