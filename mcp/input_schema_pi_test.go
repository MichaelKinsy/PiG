package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/input-schema.test.ts (Pi 76c040a2a).

func TestNormalizeMcpInputSchemaAddsTypeObjectAndEmptyPropertiesWhenMissing(t *testing.T) {
	jsonEqual(t, json.RawMessage(mcp.NormalizeMcpInputSchema(json.RawMessage(`{}`))), `{"type":"object","properties":{}}`)
	jsonEqual(t, json.RawMessage(mcp.NormalizeMcpInputSchema(json.RawMessage(`{"type":"object"}`))), `{"type":"object","properties":{}}`)
}

func TestNormalizeMcpInputSchemaKeepsTheSchemasOwnKeywords(t *testing.T) {
	schema := `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`
	jsonEqual(t, json.RawMessage(mcp.NormalizeMcpInputSchema(json.RawMessage(schema))), schema)
	jsonEqual(t, json.RawMessage(mcp.NormalizeMcpInputSchema(json.RawMessage(`{"properties":{"a":{}},"$defs":{"x":{}}}`))), `{"type":"object","properties":{"a":{}},"$defs":{"x":{}}}`)
}
