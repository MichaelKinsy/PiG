package codemode

import "testing"

// Pi 1.1.0 (probed through RPC get_messages, system message toolsAdded) declares the codemode tool's parameters in TypeBox's key order: type, required, properties, and type before description.
func TestInputSchemaKeepsTypeBoxKeyOrder(t *testing.T) {
	const want = `{"type":"object","required":["code"],"properties":{"code":{"type":"string","description":"Raw JavaScript source."}}}`
	if string(inputSchema) != want {
		t.Fatalf("inputSchema = %s\nwant        %s", inputSchema, want)
	}
}
