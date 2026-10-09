package mcp_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports the error classes of packages/mcp/src/protocol/jsonrpc.ts:45-79 and transports/streamable-http.ts:126-153:
// constructor arguments, `name`, `message` and `cause` (never set upstream).
func TestErrorClassesCarryUpstreamNamesMessagesAndFields(t *testing.T) {
	resp := &http.Response{Header: http.Header{"Www-Authenticate": {"Bearer"}}}
	cases := []struct {
		err     interface{ Error() string }
		name    string
		message string
	}{
		{mcp.NewMcpError(-32600, "bad", nil), "McpError", "bad"},
		{mcp.NewMcpConnectionClosedError(""), "McpConnectionClosedError", "MCP connection closed"},
		{mcp.NewMcpTimeoutError(5), "McpTimeoutError", "MCP request timed out after 5ms"},
		{mcp.NewMcpAbortError(""), "AbortError", "MCP request aborted"},
		{mcp.NewMcpHttpError(500, "boom", "b"), "McpHttpError", "boom"},
		{mcp.NewMcpAuthRequiredError(resp, ""), "McpAuthRequiredError", "MCP server requires authentication"},
		{mcp.NewMcpSessionExpiredError(""), "McpSessionExpiredError", "MCP session expired"},
	}
	for _, c := range cases {
		named := c.err.(interface {
			Name() string
			Cause() error
		})
		if named.Name() != c.name || c.err.Error() != c.message || named.Cause() != nil {
			t.Errorf("%T: name=%q message=%q cause=%v, want %q %q nil", c.err, named.Name(), c.err.Error(), named.Cause(), c.name, c.message)
		}
	}
	if e := mcp.NewMcpError(-32602, "m", json.RawMessage(`{"k":1}`)); e.Code != -32602 || string(e.Data) != `{"k":1}` {
		t.Errorf("McpError code/data = %d %s", e.Code, e.Data)
	}
	if e := mcp.NewMcpConnectionClosedError("gone"); e.Error() != "gone" {
		t.Errorf("McpConnectionClosedError message = %q", e.Error())
	}
	if e := mcp.NewMcpAbortError("stop"); e.Error() != "stop" {
		t.Errorf("McpAbortError message = %q", e.Error())
	}
	if e := mcp.NewMcpTimeoutError(5); e.TimeoutMs != 5 {
		t.Errorf("McpTimeoutError timeoutMs = %d", e.TimeoutMs)
	}
	if e := mcp.NewMcpHttpError(500, "boom", "b"); e.Status != 500 || e.Body != "b" {
		t.Errorf("McpHttpError status/body = %d %q", e.Status, e.Body)
	}
	// wwwAuthenticate is response.headers.get("www-authenticate"): the joined header, or null when absent.
	if e := mcp.NewMcpAuthRequiredError(resp, "x"); e.Status != 401 || e.Body != "x" || !e.HasWWWAuthenticate || e.WWWAuthenticate != "Bearer" {
		t.Errorf("McpAuthRequiredError = %+v", e)
	}
	if e := mcp.NewMcpAuthRequiredError(&http.Response{Header: http.Header{}}, ""); e.HasWWWAuthenticate {
		t.Errorf("McpAuthRequiredError without header = %+v", e)
	}
	if e := mcp.NewMcpSessionExpiredError("y"); e.Status != 404 || e.Body != "y" {
		t.Errorf("McpSessionExpiredError status/body = %d %q", e.Status, e.Body)
	}
	if m := mcp.NewMcpTimeoutError(7).Message(); m != "MCP request timed out after 7ms" {
		t.Errorf("message = %q", m)
	}
}
