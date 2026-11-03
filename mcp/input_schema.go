package mcp

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Ports packages/mcp/src/input-schema.ts.
//
// An MCP tool's input schema in the shape LLM tool APIs accept.

// NormalizeMcpInputSchema is the tool's input schema as an object schema.
// Tool parameters must be objects, MCP servers may omit `type`, and some
// providers reject object schemas without `properties`. Other keywords are
// kept, in their order; a schema that is not an object reads as an empty one.
func NormalizeMcpInputSchema(schema json.RawMessage) json.RawMessage {
	obj, err := orderedjson.Parse(schema)
	if err != nil {
		obj = orderedjson.New()
	}
	// `schema.type ?? "object"`: null counts as missing.
	if raw, ok := obj.Get("type"); !ok || string(raw) == "null" {
		_ = obj.SetValue("type", "object")
	}
	if !obj.Has("properties") {
		obj.Set("properties", json.RawMessage("{}"))
	}
	raw, _ := obj.MarshalJSON()
	return raw
}
