package mcp

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Ports packages/mcp/src/input-schema.ts (Pi 76c040a2a, moved from coding-agent's extensions/mcp/tools.ts toParameters).

// NormalizeMcpInputSchema is the tool's input schema as an object schema. Tool parameters must be objects, MCP servers may omit `type`, and some
// providers reject object schemas without `properties`. Other keywords are kept.
func NormalizeMcpInputSchema(schema json.RawMessage) json.RawMessage {
	obj, err := orderedjson.Parse(schema)
	if err != nil {
		obj = orderedjson.New()
	}
	if raw, ok := obj.Get("type"); !ok || string(raw) == "null" {
		_ = obj.SetValue("type", "object")
	}
	if !obj.Has("properties") {
		obj.Set("properties", json.RawMessage("{}"))
	}
	raw, _ := obj.MarshalJSON()
	return raw
}
