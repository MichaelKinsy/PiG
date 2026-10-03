package extension

import (
	"encoding/json"
	"testing"
)

// The servers every extension process receives (the state push, mcp_servers_change and the Go/Rust/Python SDKs' getMcpServers) are this list marshaled. Pi's McpServerRegistry.list() returns structuredClone of the object the extension registered, so JSON.stringify of it keeps every member, known or not, in the order written (integer-like keys first). The expectation is the output of real Pi 0.99.1 (McpServerRegistry and validateMcpServerConfig of dist/core/mcp-servers.js).
//
// upstream: packages/coding-agent/src/core/mcp-servers.ts:151-222 (validateMcpServerConfig returns `value`), 283 (list)
func TestRegisteredMcpServersKeepTheConfigAsRegistered(t *testing.T) {
	runtime := CreateExtensionRuntime()
	registrations := []struct{ name, config string }{
		{"ordered", `{"timeout":5,"url":"https://mcp.example/x","toolExposure":{"b*":"direct","a":"hidden","2":"direct"},"headers":{"X-Z":"1","A":"2"},"enabled":true,"exposure":"direct","foo":{"zz":1,"aa":[{"y":1,"b":2}]},"oauth":{"scope":"s","clientId":"c"}}`},
		{"stdio_one", `{"args":["--x"],"env":{"Z":"1","A":"2"},"cwd":".","command":"mcp-bin","type":"stdio"}`},
	}
	for _, registration := range registrations {
		if err := runtime.RegisterMcpServer("/ext/a.mjs", registration.name, json.RawMessage(registration.config)); err != nil {
			t.Fatal(err)
		}
	}
	const want = `[{"name":"ordered","config":{"timeout":5,"url":"https://mcp.example/x","toolExposure":{"2":"direct","b*":"direct","a":"hidden"},"headers":{"X-Z":"1","A":"2"},"enabled":true,"exposure":"direct","foo":{"zz":1,"aa":[{"y":1,"b":2}]},"oauth":{"scope":"s","clientId":"c"}},"extensionPath":"/ext/a.mjs"},{"name":"stdio_one","config":{"args":["--x"],"env":{"Z":"1","A":"2"},"cwd":".","command":"mcp-bin","type":"stdio"},"extensionPath":"/ext/a.mjs"}]`
	for range 2 {
		// A second read proves the list is a copy that keeps the same bytes.
		got, err := json.Marshal(runtime.McpServers())
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("servers\n got  %s\n want %s", got, want)
		}
	}
}
