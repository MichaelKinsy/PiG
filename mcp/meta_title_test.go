package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// A server's `title` on its implementation and the `_meta` of its read and call results reach the host unchanged; list pages drop `_meta`, as Pi's page helpers do (client.ts:306,332).
// Pi: packages/mcp/src/protocol/types.ts:15 (title), packages/mcp/src/protocol/content.ts:11 (_meta), packages/mcp/src/client.ts:306 (_meta)
// mutation-checked: dropping the `_meta` json tag of a result type, or the `title` tag of Implementation, fails it
func TestClientKeepsServerTitleAndResultMeta(t *testing.T) {
	clientTransport, server := createServer(t)
	server.setHandler("initialize", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"resources": map[string]any{}},
			"serverInfo": map[string]any{"name": "titled", "version": "1.0.0", "title": "Titled Server"}}, nil
	})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "c", Version: "1", Title: "Client Title"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if info := client.ServerInfo(); info == nil || info.Title != "Titled Server" {
		t.Fatalf("server info = %+v, want the server's title", info)
	}
	if clientInfo, _ := paramsOf(t, lastRecorded(t, server, "initialize"))["clientInfo"].(map[string]any); clientInfo["title"] != "Client Title" {
		t.Fatalf("clientInfo = %v, want the client's title sent", clientInfo)
	}
	meta := `{"trace":"abc","n":1}`
	server.setHandler("resources/list", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"resources": []any{}, "_meta": json.RawMessage(meta)}, nil
	})
	server.setHandler("resources/templates/list", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"resourceTemplates": []any{}, "_meta": json.RawMessage(meta)}, nil
	})
	server.setHandler("resources/read", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"contents": []any{}, "_meta": json.RawMessage(meta)}, nil
	})
	server.setHandler("tools/call", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"content": []any{}, "_meta": json.RawMessage(meta)}, nil
	})
	resources, err := client.ListResourcesPage(t.Context(), nil, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resources.Meta != nil { // Pi's listResourcesPage returns { resources, ...pageCursor(page) }: the page's _meta is not kept
		t.Fatalf("resources page _meta = %s, want it dropped as Pi drops it", resources.Meta)
	}
	templates, err := client.ListResourceTemplatesPage(t.Context(), nil, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if templates.Meta != nil {
		t.Fatalf("templates page _meta = %s, want it dropped as Pi drops it", templates.Meta)
	}
	read, err := client.ReadResource(t.Context(), "file:///x", mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, read.Meta, meta)
	call, err := client.CallTool(t.Context(), "any", nil, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, call.Meta, meta)
}

// Every member of a listed tool, resource and resource template, and of the server capabilities, survives the client's decoding.
// Pi: packages/mcp/src/protocol/types.ts:83 (execution), packages/mcp/src/protocol/types.ts:84 (_meta), packages/mcp/src/protocol/types.ts:96 (title), packages/mcp/src/protocol/types.ts:99 (mimeType), packages/mcp/src/protocol/types.ts:100 (size), packages/mcp/src/protocol/types.ts:101 (annotations), packages/mcp/src/protocol/types.ts:32 (logging), packages/mcp/src/protocol/types.ts:33 (prompts), packages/mcp/src/protocol/types.ts:36 (completions), packages/mcp/src/protocol/types.ts:31 (experimental)
// mutation-checked: renaming the json tag of Tool.Execution, Resource.Size, any Meta, Title, Description or Annotations, or a ServerCapabilities member fails it
func TestClientDecodesEveryListedMemberAndServerCapability(t *testing.T) {
	clientTransport, server := createServer(t)
	server.setHandler("initialize", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "s", "version": "1"},
			"capabilities": map[string]any{"experimental": map[string]any{"x": 1}, "logging": map[string]any{}, "prompts": map[string]any{"listChanged": true},
				"completions": map[string]any{}, "resources": map[string]any{}}}, nil
	})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "c", Version: "1"}})
	if _, err := client.Connect(t.Context(), clientTransport); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	caps := client.ServerCapabilities()
	if caps == nil || caps.Experimental["x"] != float64(1) || caps.Logging == nil || caps.Prompts == nil || caps.Prompts.ListChanged == nil || !*caps.Prompts.ListChanged || caps.Completions == nil {
		t.Fatalf("capabilities = %+v", caps)
	}
	annotations := map[string]any{"audience": []any{"user"}, "priority": 0.5}
	server.setHandler("tools/list", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"tools": []any{map[string]any{"name": "t", "inputSchema": map[string]any{"type": "object"}, "execution": map[string]any{"taskSupport": "optional"}, "_meta": map[string]any{"k": "v"}}}}, nil
	})
	server.setHandler("resources/list", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"resources": []any{map[string]any{"uri": "file:///a", "name": "a", "title": "A", "description": "desc", "size": 12, "annotations": annotations, "_meta": map[string]any{"k": "v"}}}}, nil
	})
	server.setHandler("resources/templates/list", func(mcp.JSONRPCMessage) (any, error) {
		return map[string]any{"resourceTemplates": []any{map[string]any{"uriTemplate": "file:///{p}", "name": "tpl", "title": "T", "description": "d", "annotations": annotations, "_meta": map[string]any{"k": "v"}}}}, nil
	})
	tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
	if err != nil || len(tools) != 1 || tools[0].Execution == nil || tools[0].Execution.TaskSupport != "optional" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	jsonEqual(t, tools[0].Meta, `{"k":"v"}`)
	resources, err := client.ListResources(t.Context(), mcp.RequestOptions{})
	if err != nil || len(resources) != 1 {
		t.Fatalf("resources = %+v, %v", resources, err)
	}
	resource := resources[0]
	if resource.Title != "A" || resource.Description != "desc" || resource.Size == nil || *resource.Size != 12 || resource.Annotations == nil {
		t.Fatalf("resource = %+v", resource)
	}
	jsonEqual(t, resource.Meta, `{"k":"v"}`)
	jsonEqual(t, resource.Annotations, `{"audience":["user"],"priority":0.5}`)
	templates, err := client.ListResourceTemplates(t.Context(), mcp.RequestOptions{})
	if err != nil || len(templates) != 1 {
		t.Fatalf("templates = %+v, %v", templates, err)
	}
	template := templates[0]
	if template.Title != "T" || template.Description != "d" || template.Annotations == nil {
		t.Fatalf("template = %+v", template)
	}
	jsonEqual(t, template.Meta, `{"k":"v"}`)
}
