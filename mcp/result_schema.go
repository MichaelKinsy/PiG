package mcp

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Ports packages/mcp/src/result-schema.ts (Pi 1acc72acd).
//
// The output schema of an MCP tool for programs that call it: the whole `CallToolResult`, with the tool's own output
// schema as `structuredContent`.

// contentBlockSchema is MCP content blocks as one compact type with the fields of
// text and image blocks, which scripts read most (`image(block)` takes an image
// block as is). Rendered into every MCP tool's declaration.
const contentBlockSchema = `{"type":"object","properties":{"type":{"enum":["text","image","audio","resource_link","resource"]},"text":{"type":"string"},"data":{"type":"string"},"mimeType":{"type":"string"}},"required":["type"],"additionalProperties":true}`

// isSchemaDataKeyword reports the keywords whose values are data, so a `$ref` key
// in them is not a reference. isSchemaMapKeyword reports the keywords whose
// values map names to schemas, so their keys are names, not keywords. A switch,
// unlike a map, costs package initialization nothing.
func isSchemaDataKeyword(key string) bool {
	switch key {
	case "const", "enum", "default", "examples":
		return true
	}
	return false
}

func isSchemaMapKeyword(key string) bool {
	switch key {
	case "properties", "patternProperties", "dependentSchemas", "dependencies", "$defs", "definitions":
		return true
	}
	return false
}

// relocateSchema is schema as a subschema at the JSON pointer pointer
// (`#/properties/structuredContent`) of another schema: its local references
// (`#`, `#/$defs/Item`) are rewritten to point below pointer, so they resolve to
// the same subschemas as before. Key order is kept.
func relocateSchema(schema json.RawMessage, pointer string) json.RawMessage {
	trimmed := bytes.TrimSpace(schema)
	if len(trimmed) == 0 {
		return schema
	}
	switch trimmed[0] {
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(trimmed, &items) != nil {
			return schema
		}
		for i, item := range items {
			items[i] = relocateSchema(item, pointer)
		}
		//portlint:allow nilslicenull result-schema.ts:64 schema.map(...) of an array: items was decoded from text starting with '[', and Unmarshal makes [] an empty non-nil slice, so it encodes as [] like Pi
		raw, err := json.Marshal(items)
		if err != nil {
			return schema
		}
		return raw
	case '{':
		object, err := orderedjson.Parse(trimmed)
		if err != nil {
			return schema
		}
		for _, key := range object.Keys() {
			value, _ := object.Get(key)
			var ref string
			switch {
			case key == "$ref" && json.Unmarshal(value, &ref) == nil && (ref == "#" || strings.HasPrefix(ref, "#/")):
				_ = object.SetValue(key, pointer+ref[1:])
			case isSchemaDataKeyword(key):
			case isSchemaMapKeyword(key) && orderedjson.IsObject(value):
				names, _ := orderedjson.Parse(value)
				for _, name := range names.Keys() {
					subschema, _ := names.Get(name)
					names.Set(name, relocateSchema(subschema, pointer))
				}
				raw, _ := names.MarshalJSON()
				object.Set(key, raw)
			default:
				object.Set(key, relocateSchema(value, pointer))
			}
		}
		raw, _ := object.MarshalJSON()
		return raw
	}
	return schema
}

// CreateMcpResultSchema is the output schema of an MCP tool: the `CallToolResult`
// programs receive (without `_meta`, which callers drop), with the tool's own output
// schema as `structuredContent`. Without one, structured content may be any JSON value.
func CreateMcpResultSchema(structuredContentSchema json.RawMessage) json.RawMessage {
	properties := orderedjson.New()
	properties.Set("content", json.RawMessage(`{"type":"array","items":`+contentBlockSchema+`}`))
	// Servers may send structured content without declaring a schema for it, and it may be any JSON value.
	trimmed := bytes.TrimSpace(structuredContentSchema)
	if len(trimmed) > 0 && jsTruthy(trimmed) {
		properties.Set("structuredContent", relocateSchema(trimmed, "#/properties/structuredContent"))
	} else {
		properties.Set("structuredContent", json.RawMessage(`{}`))
	}
	properties.Set("isError", json.RawMessage(`{"type":"boolean"}`))
	schema := orderedjson.New()
	_ = schema.SetValue("type", "object")
	propertiesRaw, _ := properties.MarshalJSON()
	schema.Set("properties", propertiesRaw)
	_ = schema.SetValue("required", []string{"content"})
	// Fields newer protocol versions add.
	_ = schema.SetValue("additionalProperties", true)
	raw, _ := schema.MarshalJSON()
	return raw
}
