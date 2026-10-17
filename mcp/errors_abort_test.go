package mcp_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// packages/mcp/src/protocol/jsonrpc.ts McpAbortError: `new McpAbortError()` has the message "MCP request aborted", an explicit message is kept, the name is
// "AbortError" and there is no cause.
func TestMcpAbortErrorHasPiDefaultsNameAndNoCause(t *testing.T) {
	abort := mcp.NewMcpAbortError("")
	if abort.Error() != "MCP request aborted" || abort.Name() != "AbortError" || abort.Cause() != nil {
		t.Fatalf("default abort error = %q %q %v", abort.Error(), abort.Name(), abort.Cause())
	}
	custom := mcp.NewMcpAbortError("stopped by the caller")
	if custom.Error() != "stopped by the caller" {
		t.Fatalf("custom abort message = %q", custom.Error())
	}
	var target *mcp.McpAbortError
	if !errors.As(error(custom), &target) || target != custom {
		t.Fatal("errors.As does not find the abort error")
	}
}
