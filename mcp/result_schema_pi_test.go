package mcp_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/result-schema.test.ts (Pi 1acc72acd).

func TestCreateMcpResultSchemaDeclaresTheCallToolResultWithAnyStructuredContentWithoutAnOutputSchema(t *testing.T) {
	raw := mcp.CreateMcpResultSchema(nil)
	root, err := orderedjson.Parse(raw)
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}
	propsRaw, ok := root.Get("properties")
	if !ok {
		t.Fatalf("missing properties in %s", raw)
	}
	props, err := orderedjson.Parse(propsRaw)
	if err != nil {
		t.Fatalf("parse properties: %v", err)
	}
	if got, want := props.Keys(), []string{"content", "structuredContent", "isError"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("property keys = %v, want %v", got, want)
	}
	sc, ok := props.Get("structuredContent")
	if !ok {
		t.Fatalf("missing structuredContent in %s", propsRaw)
	}
	jsonEqual(t, sc, `{}`)

	var decoded struct {
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(decoded.Required, []string{"content"}) {
		t.Fatalf("required = %v, want [content]", decoded.Required)
	}
	if !decoded.AdditionalProperties {
		t.Fatalf("additionalProperties = false, want true")
	}
}

func TestCreateMcpResultSchemaMovesLocalReferencesBelowStructuredContentKeepingDataAndRemoteReferences(t *testing.T) {
	hit := `{"type":"object","properties":{"id":{"type":"string"}}}`
	raw := mcp.CreateMcpResultSchema(json.RawMessage(`{
		"type": "object",
		"properties": {
			"hits": {"type": "array", "items": {"$ref": "#/$defs/Hit"}},
			"children": {"type": "array", "items": {"$ref": "#"}},
			"default": {"$ref": "#/definitions/Hit"},
			"remote": {"$ref": "https://example.com/schema.json"}
		},
		"$defs": {"Hit": {"type":"object","properties":{"id":{"type":"string"}},"default":{"$ref":"#/stays"}}},
		"definitions": {"Hit": ` + hit + `}
	}`))
	var decoded struct {
		Properties struct {
			StructuredContent struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Defs       map[string]struct {
					Default json.RawMessage `json:"default"`
				} `json:"$defs"`
			} `json:"structuredContent"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	moved := decoded.Properties.StructuredContent
	at := "#/properties/structuredContent"
	jsonEqual(t, moved.Properties["hits"], `{"type":"array","items":{"$ref":"`+at+`/$defs/Hit"}}`)
	jsonEqual(t, moved.Properties["children"], `{"type":"array","items":{"$ref":"`+at+`"}}`)
	jsonEqual(t, moved.Properties["default"], `{"$ref":"`+at+`/definitions/Hit"}`)
	jsonEqual(t, moved.Properties["remote"], `{"$ref":"https://example.com/schema.json"}`)
	jsonEqual(t, moved.Defs["Hit"].Default, `{"$ref":"#/stays"}`)
}

func TestCreateMcpResultSchemaTreatsFalsyOutputSchemasAsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{"nil", nil, `{}`},
		{"null", json.RawMessage("null"), `{}`},
		{"false", json.RawMessage("false"), `{}`},
		{"0", json.RawMessage("0"), `{}`},
		{"-0", json.RawMessage("-0"), `{}`},
		{" 0.0 ", json.RawMessage(" 0.0 "), `{}`},
		{`""`, json.RawMessage(`""`), `{}`},
		{"true", json.RawMessage("true"), "true"},
		{"[]", json.RawMessage("[]"), "[]"},
		{"1", json.RawMessage("1"), "1"},
		{`"x"`, json.RawMessage(`"x"`), `"x"`},
	} {
		schema := mcp.CreateMcpResultSchema(tc.raw)
		var decoded struct {
			Properties struct {
				StructuredContent json.RawMessage `json:"structuredContent"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(schema, &decoded); err != nil {
			t.Fatalf("unmarshal for %s: %v", tc.name, err)
		}
		jsonEqual(t, decoded.Properties.StructuredContent, tc.want)
	}
}
