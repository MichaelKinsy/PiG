package mcp_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// upstream: packages/mcp/src/protocol/jsonrpc.ts:64-72 McpTimeoutError carries timeoutMs and the message "MCP request timed out after <n>ms".
func TestNewMcpTimeoutError(t *testing.T) {
	err := mcp.NewMcpTimeoutError(1500)
	if err.TimeoutMs != 1500 || err.Error() != "MCP request timed out after 1500ms" {
		t.Fatalf("error = %+v %q", err, err.Error())
	}
	var wrapped error = err
	if got, ok := errors.AsType[*mcp.McpTimeoutError](wrapped); !ok || got.TimeoutMs != 1500 {
		t.Fatal("the constructor result must match errors.As on *McpTimeoutError")
	}
}
