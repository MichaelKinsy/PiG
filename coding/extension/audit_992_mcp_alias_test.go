package extension

import (
	"encoding/json"
	"testing"
)

// Pi 0.99.2 validateMcpServerConfig returns a copy of the entry with exposure aliases resolved
// (.upstream/v0.99.2/packages/coding-agent/src/core/mcp-servers.ts:151-166 resolveExposureAliases, 192-196), and
// registerMcpServer stores structuredClone of that copy (core/extensions/loader.ts:466-480), so pi.getMcpServers() and
// mcp_servers_change report `codemode` for a server registered with `codemode-deferred`. Probe of real Pi 0.99.2 (print
// mode, extension command printing JSON.stringify(pi.getMcpServers())):
//
//	[{"name":"auditsrv","config":{"command":"true","exposure":"codemode","toolExposure":{"a":"codemode"},"enabled":false},"extensionPath":...}]
//
// PiG 0.4.0 candidate printed `"exposure":"codemode-deferred","toolExposure":{"a":"codemode-deferred"}`.
func TestAuditRegisteredMcpServerReportsResolvedExposureAliases(t *testing.T) {
	runtime := CreateExtensionRuntime()
	config := `{"command":"true","exposure":"codemode-deferred","toolExposure":{"a":"codemode-deferred"},"enabled":false}`
	if err := runtime.RegisterMcpServer("/ext/a.mjs", "auditsrv", json.RawMessage(config)); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(runtime.McpServers())
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"name":"auditsrv","config":{"command":"true","exposure":"codemode","toolExposure":{"a":"codemode"},"enabled":false},"extensionPath":"/ext/a.mjs"}]`
	if string(got) != want {
		t.Fatalf("servers\n got  %s\n want %s", got, want)
	}
}
