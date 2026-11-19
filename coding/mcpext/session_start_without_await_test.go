package mcpext_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// The built-in MCP extension's session_start handler returns without awaiting in Pi (extensions/mcp/index.ts:1115-1152: a plain function that starts the connections
// without awaiting them), so a runner that holds it has no session_start handler that can pend; the handlers that Pi's extension registers as async
// (before_agent_start :1179, tool_call :1192, turn_start :1213, mcp_servers_change :1218, session_shutdown :1253) are registered as any handler.
func TestBuiltinMCPSessionStartHandlerReturnsWithoutAwaiting(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	loaded, err := factoryload.LoadExtensionFromFactory(mcpext.CreateMcpExtension(mcpext.Options{LoadConfig: func(context.Context) mcpext.LoadedMcpConfig { return mcpext.LoadedMcpConfig{} }}), t.TempDir(), extension.CreateEventBus(), runtime, "builtin:mcp")
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{loaded}, t.TempDir(), runtime)
	if !runner.HasHandlers("session_start") || runner.HasHandlersThatMayAwait("session_start") {
		t.Fatalf("session_start: HasHandlers = %v, HasHandlersThatMayAwait = %v, want true, false", runner.HasHandlers("session_start"), runner.HasHandlersThatMayAwait("session_start"))
	}
	for _, event := range []string{"before_agent_start", "tool_call", "turn_start", "mcp_servers_change", "session_shutdown"} {
		if !runner.HasHandlersThatMayAwait(event) {
			t.Errorf("%s: the handler is not registered, or is registered as one that returns without awaiting", event)
		}
	}
}
