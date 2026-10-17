package toolsearch

import "testing"

// Pi 1.1.0 (probed through RPC get_messages, system message toolsAdded) declares the tool_search parameters in TypeBox's key order: type, required, properties, and type before description.
func TestInputSchemaKeepsTypeBoxKeyOrder(t *testing.T) {
	const want = `{"type":"object","required":["query"],"properties":{"query":{"type":"string","description":"Search query for deferred tools."},"limit":{"type":"number","description":"Maximum number of tools to return. Defaults to 8."}}}`
	if string(inputSchema) != want {
		t.Fatalf("inputSchema = %s\nwant        %s", inputSchema, want)
	}
}
