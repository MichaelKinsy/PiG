package mcp_test

import (
	"encoding/json"
	"strings"
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

// upstream packages/mcp/src/client.ts toResource, toResourceTemplate, validateReadResourceResult: list entries and read
// contents reach the caller as the server sent them, `name` defaulting to the URI (or URI template) when absent.
func TestClientKeepsTheEntriesOfResourceListsAndReadsAsTheServerSentThem(t *testing.T) {
	client, server := connect(t)
	server.setHandler("resources/list", func(mcp.JSONRPCMessage) (any, error) {
		return json.RawMessage(`{"resources":[{"mimeType":"text/plain","uri":"a://1","x":{"k":[1,2]}},{"name":"named","uri":"a://2"}]}`), nil
	})
	server.setHandler("resources/templates/list", func(mcp.JSONRPCMessage) (any, error) {
		return json.RawMessage(`{"resourceTemplates":[{"z":1,"uriTemplate":"a://{x}"},{"uriTemplate":"b://{x}","name":"b"}]}`), nil
	})
	server.setHandler("resources/read", func(mcp.JSONRPCMessage) (any, error) {
		return json.RawMessage(`{"contents":[{"text":"t","uri":"a://1","x":1}]}`), nil
	})
	resources, err := client.ListResources(t.Context(), mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(resources[0].Raw); got != `{"mimeType":"text/plain","uri":"a://1","x":{"k":[1,2]},"name":"a://1"}` {
		t.Fatalf("resource 0 = %s", got)
	}
	if got := string(resources[1].Raw); got != `{"name":"named","uri":"a://2"}` {
		t.Fatalf("resource 1 = %s", got)
	}
	templates, err := client.ListResourceTemplates(t.Context(), mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(templates[0].Raw); got != `{"z":1,"uriTemplate":"a://{x}","name":"a://{x}"}` {
		t.Fatalf("template 0 = %s", got)
	}
	if got := string(templates[1].Raw); got != `{"uriTemplate":"b://{x}","name":"b"}` {
		t.Fatalf("template 1 = %s", got)
	}
	read, err := client.ReadResource(t.Context(), "a://1", mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(read.Contents[0].Raw); got != `{"text":"t","uri":"a://1","x":1}` {
		t.Fatalf("contents 0 = %s", got)
	}
}

// The `title` of a tool or content block is optional: a server's empty title is present, a null or missing one is not.
func TestToolsAndContentBlocksTrackWhetherTheServerSentATitle(t *testing.T) {
	for raw, want := range map[string]bool{`{"name":"x","inputSchema":{},"title":""}`: true, `{"name":"x","inputSchema":{},"title":"T"}`: true, `{"name":"x","inputSchema":{},"title":null}`: false, `{"name":"x","inputSchema":{}}`: false} {
		var tool mcp.Tool
		if err := json.Unmarshal([]byte(raw), &tool); err != nil || tool.HasTitle != want {
			t.Errorf("tool %s: HasTitle = %v, %v; want %v", raw, tool.HasTitle, err, want)
		}
	}
	for raw, want := range map[string]bool{`{"type":"resource_link","uri":"a","name":"n","title":""}`: true, `{"type":"resource_link","uri":"a","name":"n","title":null}`: false, `{"type":"resource_link","uri":"a","name":"n"}`: false} {
		var block mcp.ContentBlock
		if err := json.Unmarshal([]byte(raw), &block); err != nil || block.HasTitle != want {
			t.Errorf("block %s: HasTitle = %v, %v; want %v", raw, block.HasTitle, err, want)
		}
	}
	// A block built in Go with an explicit empty title writes it.
	built, err := json.Marshal(mcp.ContentBlock{Type: "resource_link", URI: "a", Name: "n", HasTitle: true})
	if err != nil || !strings.Contains(string(built), `"title":""`) {
		t.Errorf("built block = %s, %v", built, err)
	}
}
