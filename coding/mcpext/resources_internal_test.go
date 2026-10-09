package mcpext

// pi: packages/coding-agent/src/extensions/mcp/resources.ts

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/mcp"
)

// fakeResourceServer is a McpResourceServer with fixed pages.
type fakeResourceServer struct {
	name      string
	resources []mcp.Resource
	contents  []mcp.ResourceContents
	cursors   []*string
}

func (s *fakeResourceServer) Name() string           { return s.name }
func (s *fakeResourceServer) Timeout() time.Duration { return time.Second }
func (s *fakeResourceServer) ResourcesPage(_ context.Context, cursor *string, _ mcp.RequestOptions) (*mcp.ListResourcesResult, error) {
	s.cursors = append(s.cursors, cursor)
	return &mcp.ListResourcesResult{Resources: s.resources, NextCursor: "next-" + s.name}, nil
}
func (s *fakeResourceServer) ResourceTemplatesPage(context.Context, *string, mcp.RequestOptions) (*mcp.ListResourceTemplatesResult, error) {
	return &mcp.ListResourceTemplatesResult{}, nil
}
func (s *fakeResourceServer) AllResources(context.Context, mcp.RequestOptions) ([]mcp.Resource, error) {
	return s.resources, nil
}
func (s *fakeResourceServer) AllResourceTemplates(context.Context, mcp.RequestOptions) ([]mcp.ResourceTemplate, error) {
	return nil, nil
}
func (s *fakeResourceServer) ReadResource(context.Context, string, mcp.RequestOptions) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{Contents: s.contents}, nil
}

func resourceTool(t *testing.T, servers []McpResourceServer, name string) extension.ToolDefinition {
	t.Helper()
	for _, definition := range CreateMcpResourceToolDefinitions(ResourceToolsOptions{Exposure: extension.McpExposureCodemode, Servers: func() []McpResourceServer { return servers }}) {
		if definition.Name == name {
			return definition
		}
	}
	t.Fatalf("no tool %s", name)
	return extension.ToolDefinition{}
}

func runResourceTool(t *testing.T, servers []McpResourceServer, name, params string) (agent.AgentToolResult, error) {
	t.Helper()
	raw, err := resourceTool(t, servers, name).Execute(t.Context(), "call", json.RawMessage(params), nil)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	return raw, nil
}

// resources.ts:170-237,303-323: argument and server errors, the server list in the unknown-server message, the cursor rule, the page cursor handed to the server, and the read tool's labels and empty result.
func TestResourceToolsErrorsListingsAndReads(t *testing.T) {
	text := func(s string) *string { return &s }
	zeta := &fakeResourceServer{name: "zeta", resources: []mcp.Resource{{URI: "z://1", Name: "one"}}, contents: []mcp.ResourceContents{{URI: "z://a", Text: text("alpha")}, {URI: "z://b", Text: text("beta")}}}
	alpha := &fakeResourceServer{name: "Alpha", resources: []mcp.Resource{{URI: "a://1", Name: "uno"}}}
	servers := []McpResourceServer{zeta, alpha}

	for _, tc := range []struct{ tool, params, want string }{
		{ListMcpResourcesTool, `{"server":"nope"}`, `MCP server "nope" has no resources. Servers with resources: zeta, Alpha`},
		{ListMcpResourcesTool, `{"cursor":"c"}`, "cursor can only be used when a server is specified"},
		{ListMcpResourcesTool, `{"server":1}`, "server must be a string"},
		{ReadMcpResourceTool, `{"uri":"x://y"}`, "server must be provided"},
		{ReadMcpResourceTool, `{"server":"zeta"}`, "uri must be provided"},
		{ReadMcpResourceTool, `{"server":"nope","uri":"x://y"}`, `MCP server "nope" has no resources. Servers with resources: zeta, Alpha`},
	} {
		if _, err := runResourceTool(t, servers, tc.tool, tc.params); err == nil || err.Error() != tc.want {
			t.Errorf("%s %s: error = %v, want %q", tc.tool, tc.params, err, tc.want)
		}
	}
	if _, err := runResourceTool(t, nil, ListMcpResourcesTool, `{"server":"nope"}`); err == nil || err.Error() != `MCP server "nope" has no resources` {
		t.Errorf("no servers: error = %v, want the bare message", err)
	}

	// One server: one page, the cursor passes through, and nextCursor is reported.
	page, err := runResourceTool(t, servers, ListMcpResourcesTool, `{"server":"zeta","cursor":"c1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(zeta.cursors) != 1 || zeta.cursors[0] == nil || *zeta.cursors[0] != "c1" {
		t.Fatalf("page cursor = %v, want c1", zeta.cursors)
	}
	var listed struct {
		Server     string
		Resources  []struct{ Server, URI string }
		NextCursor string
	}
	if err := json.Unmarshal(page.StructuredContent, &listed); err != nil {
		t.Fatal(err)
	}
	if listed.Server != "zeta" || len(listed.Resources) != 1 || listed.Resources[0].Server != "zeta" || listed.Resources[0].URI != "z://1" || listed.NextCursor != "next-zeta" {
		t.Fatalf("one-server listing = %+v", listed)
	}

	// Every server: locale order (Alpha before zeta), no cursor.
	all, err := runResourceTool(t, servers, ListMcpResourcesTool, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var everything struct{ Resources []struct{ Server string } }
	if err := json.Unmarshal(all.StructuredContent, &everything); err != nil {
		t.Fatal(err)
	}
	if len(everything.Resources) != 2 || everything.Resources[0].Server != "Alpha" || everything.Resources[1].Server != "zeta" {
		t.Fatalf("all-server listing = %+v, want Alpha then zeta", everything)
	}

	// Several contents are labeled with their URIs; an empty read reports the URI.
	read, err := runResourceTool(t, servers, ReadMcpResourceTool, `{"server":"zeta","uri":"z://dir"}`)
	if err != nil {
		t.Fatal(err)
	}
	joined := contentText(read)
	for _, want := range []string{"z://a:", "alpha", "z://b:", "beta"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("read content %q is missing %q", joined, want)
		}
	}
	empty, err := runResourceTool(t, []McpResourceServer{alpha}, ReadMcpResourceTool, `{"server":"Alpha","uri":"a://none"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := contentText(empty); got != "Resource a://none is empty." {
		t.Fatalf("empty read = %q", got)
	}
}

func contentText(result agent.AgentToolResult) string {
	var out []string
	for _, block := range result.Content {
		raw, _ := json.Marshal(block)
		var text struct{ Text string }
		_ = json.Unmarshal(raw, &text)
		out = append(out, text.Text)
	}
	return strings.Join(out, "|")
}

// resources.ts:56-80,252-308: the three tool definitions carry Pi's descriptions and parameter schemas (compared as parsed JSON, so key order is free).
func TestResourceToolDefinitionsCarryPisDescriptionsAndParameters(t *testing.T) {
	list := `{"type":"object","properties":{"server":{"type":"string","description":"MCP server name. Omit to list every server with resources."},"cursor":{"type":"string","description":"Opaque cursor from a previous call with the same server; omit for the first page."}},"additionalProperties":false}`
	read := `{"type":"object","properties":{"server":{"type":"string","description":"MCP server name exactly as configured. Must match the 'server' field returned by list_mcp_resources."},"uri":{"type":"string","description":"Resource URI to read. Must be one of the URIs returned by list_mcp_resources."}},"required":["server","uri"],"additionalProperties":false}`
	for name, want := range map[string]struct{ description, parameters string }{
		"list_mcp_resources":          {"Lists resources provided by MCP servers. Resources allow servers to share data that provides context to language models, such as files, database schemas, or application-specific information. Prefer resources over web search when possible.", list},
		"list_mcp_resource_templates": {"Lists resource templates provided by MCP servers. Parameterized resource templates allow servers to share data that takes parameters and provides context to language models, such as files, database schemas, or application-specific information. Prefer resource templates over web search when possible.", list},
		"read_mcp_resource":           {"Read a specific resource from an MCP server given the server name and resource URI.", read},
	} {
		definition := resourceTool(t, nil, name)
		if definition.Description != want.description || definition.Label != name {
			t.Errorf("%s: description %q label %q", name, definition.Description, definition.Label)
		}
		var got, expected any
		if err := json.Unmarshal(definition.Parameters, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(want.parameters), &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s: parameters\n got %s\nwant %s", name, definition.Parameters, want.parameters)
		}
		if definition.Annotations == nil || definition.Annotations.ReadOnlyHint == nil || !*definition.Annotations.ReadOnlyHint {
			t.Errorf("%s: not annotated read-only", name)
		}
	}
}
