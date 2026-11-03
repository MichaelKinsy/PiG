package mcp_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Pi packages/mcp/test/input-schema.test.ts "normalizeMcpInputSchema": packages/mcp/src/input-schema.ts. JSON.stringify of the result is its byte contract: the schema's keywords in their order, then `type` and `properties` when the schema lacked them.
// mutation-checked: a missing `type` default, a missing empty `properties`, a replaced existing `type` or `properties`, and dropped other keywords fail it.
func TestNormalizeMcpInputSchema(t *testing.T) {
	for _, tc := range []struct{ name, schema, want string }{
		{"adds type object and empty properties when missing", `{}`, `{"type":"object","properties":{}}`},
		{"adds the empty properties to a typed schema", `{"type":"object"}`, `{"type":"object","properties":{}}`},
		{"keeps the schema's own keywords", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`, `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`},
		{"adds the type after keywords the schema has", `{"properties":{"a":{}},"$defs":{"x":{}}}`, `{"properties":{"a":{}},"$defs":{"x":{}},"type":"object"}`},
		{"a null type reads as missing", `{"type":null,"properties":{"a":{}}}`, `{"type":"object","properties":{"a":{}}}`},
		{"keeps a non-object type", `{"type":"string"}`, `{"type":"string","properties":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(mcp.NormalizeMcpInputSchema([]byte(tc.schema))); got != tc.want {
				t.Errorf("NormalizeMcpInputSchema(%s) = %s, want %s", tc.schema, got, tc.want)
			}
		})
	}
}
