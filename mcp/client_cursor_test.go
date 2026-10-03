package mcp_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// client.ts validateListPage: a `null` or `""` nextCursor ends pagination instead of being followed (and failing as a
// duplicate cursor on the next empty page); any other non-string cursor is still invalid.
func TestClientEndsPaginationAtANullOrEmptyCursor(t *testing.T) {
	for _, cursor := range []any{nil, ""} {
		client, server := connect(t)
		calls := 0
		server.setHandler("tools/list", func(mcp.JSONRPCMessage) (any, error) {
			calls++
			return map[string]any{
				"tools":      []any{map[string]any{"name": "search", "inputSchema": map[string]any{"type": "object"}}},
				"nextCursor": cursor,
			}, nil
		})
		tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
		if err != nil {
			t.Fatalf("cursor %#v: %v", cursor, err)
		}
		if len(tools) != 1 || calls != 1 {
			t.Fatalf("cursor %#v: tools = %d, calls = %d", cursor, len(tools), calls)
		}
		page, err := func() (*mcp.ListResourcesResult, error) {
			server.setHandler("resources/list", func(mcp.JSONRPCMessage) (any, error) {
				return map[string]any{"resources": []any{}, "nextCursor": cursor}, nil
			})
			return client.ListResourcesPage(t.Context(), nil, mcp.RequestOptions{})
		}()
		if err != nil || page.NextCursor != "" {
			t.Fatalf("cursor %#v: page = %#v, %v", cursor, page, err)
		}
		_ = client.Close()
	}

	client, server := connect(t)
	server.setHandler("tools/list", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"tools": []any{}, "nextCursor": 0}, nil
	})
	if _, err := client.ListTools(t.Context(), mcp.RequestOptions{}); err == nil || err.Error() != "Invalid MCP tools/list cursor" {
		t.Fatalf("numeric cursor: err = %v", err)
	}
	_ = client.Close()
}
