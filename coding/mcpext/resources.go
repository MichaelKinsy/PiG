package mcpext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/coding-agent/src/extensions/mcp/resources.ts.
//
// MCP resources, through the tools Codex and opencode use:
// `list_mcp_resources`, `list_mcp_resource_templates`, and
// `read_mcp_resource`. They take a `server` argument and cover every connected
// server with resources, so models trained on those tools use them unchanged.
//
// Listings are JSON, as in Codex: `{ server?, resources: [{ server,
// ...resource }], nextCursor? }`. With a `server`, one page is listed and
// `cursor` continues it; without, every page of every server. MCP App
// resources (`ui://` URIs and `profile=mcp-app` HTML) are left out, since they
// are user interfaces for hosts that render them, and so are icons. Read
// resources become text and images for the model; binary resources are saved
// to temp files. Scripts get the JSON payloads.

// Names of the resource tools.
const (
	ListMcpResourcesTool         = "list_mcp_resources"
	ListMcpResourceTemplatesTool = "list_mcp_resource_templates"
)

// McpResourceServer is a connected server that offers resources.
type McpResourceServer interface {
	Name() string
	Timeout() time.Duration
	ResourcesPage(ctx context.Context, cursor *string, options mcp.RequestOptions) (*mcp.ListResourcesResult, error)
	ResourceTemplatesPage(ctx context.Context, cursor *string, options mcp.RequestOptions) (*mcp.ListResourceTemplatesResult, error)
	AllResources(ctx context.Context, options mcp.RequestOptions) ([]mcp.Resource, error)
	AllResourceTemplates(ctx context.Context, options mcp.RequestOptions) ([]mcp.ResourceTemplate, error)
	ReadResource(ctx context.Context, uri string, options mcp.RequestOptions) (*mcp.ReadResourceResult, error)
}

var mcpAppProfile = lazyregexp.New(`(?i);\s*profile\s*=\s*"?mcp-app"?`)

// IsMcpAppResource reports an MCP App user interface, which only hosts that
// render them can use. uri is the resource's `uri` or a template's
// `uriTemplate`.
func IsMcpAppResource(uri, mimeType string) bool {
	return strings.HasPrefix(uri, "ui://") || mcpAppProfile.MatchString(mimeType)
}

const (
	serverFilterDescription = "MCP server name. Omit to list every server with resources."
	cursorDescription       = "Opaque cursor from a previous call with the same server; omit for the first page."
)

const (
	listParameters = `{"type":"object","properties":{"server":{"type":"string","description":"` + serverFilterDescription + `"},"cursor":{"type":"string","description":"` + cursorDescription + `"}},"additionalProperties":false}`
	readParameters = `{"type":"object","properties":{"server":{"type":"string","description":"MCP server name exactly as configured. Must match the 'server' field returned by list_mcp_resources."},"uri":{"type":"string","description":"Resource URI to read. Must be one of the URIs returned by list_mcp_resources."}},"required":["server","uri"],"additionalProperties":false}`
	listingErrors  = `{"type":"array","description":"Servers that could not be listed","items":{"type":"object","properties":{"server":{"type":"string"},"error":{"type":"string"}},"required":["server","error"]}}`

	listOutputSchema = `{"type":"object","properties":{"server":{"type":"string"},"resources":{"type":"array","items":{"type":"object","properties":{"server":{"type":"string"},"uri":{"type":"string"},"name":{"type":"string"},"title":{"type":"string"},"description":{"type":"string"},"mimeType":{"type":"string"},"size":{"type":"number"}},"required":["server","uri","name"]}},"nextCursor":{"type":"string"},"errors":` + listingErrors + `},"required":["resources"]}`

	listTemplatesOutputSchema = `{"type":"object","properties":{"server":{"type":"string"},"resourceTemplates":{"type":"array","items":{"type":"object","properties":{"server":{"type":"string"},"uriTemplate":{"type":"string","description":"RFC 6570 URI template"},"name":{"type":"string"},"title":{"type":"string"},"description":{"type":"string"},"mimeType":{"type":"string"}},"required":["server","uriTemplate","name"]}},"nextCursor":{"type":"string"},"errors":` + listingErrors + `},"required":["resourceTemplates"]}`

	readOutputSchema = `{"type":"object","properties":{"server":{"type":"string"},"uri":{"type":"string"},"contents":{"type":"array","items":{"anyOf":[{"type":"object","properties":{"uri":{"type":"string"},"mimeType":{"type":"string"},"text":{"type":"string"}},"required":["uri","text"]},{"type":"object","properties":{"uri":{"type":"string"},"mimeType":{"type":"string"},"blob":{"type":"string","description":"base64"}},"required":["uri","blob"]}]}}},"required":["server","uri","contents"]}`
)

// stringArgument is upstream stringArgument: an absent or null value is "",
// a non-string value is an error, and the string is trimmed.
func stringArgument(params json.RawMessage, key string) (string, error) {
	if !isJSONObject(params) {
		return "", nil
	}
	obj, err := orderedjson.Parse(params)
	if err != nil {
		return "", nil
	}
	raw, ok := obj.Get(key)
	if !ok || string(raw) == "null" {
		return "", nil
	}
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(s), nil
}

// listed is a listed resource or template without `_meta` and icons, tagged
// with its server.
func listed(server string, item any) (json.RawMessage, error) {
	data, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	obj, err := orderedjson.Parse(data)
	if err != nil {
		return nil, err
	}
	obj.Delete("_meta")
	obj.Delete("icons")
	out := orderedjson.New()
	_ = out.SetValue("server", server)
	for _, key := range obj.Keys() {
		raw, _ := obj.Get(key)
		out.Set(key, raw)
	}
	return out.MarshalJSON()
}

func jsonResult(tool, server string, payload json.RawMessage) (agent.AgentToolResult, error) {
	content, fullOutputPath := LimitMcpContent([]ai.ToolResultMessageContent{ai.TextContent{Text: string(payload)}}, nil)
	return agent.AgentToolResult{
		Content:           content,
		Details:           McpToolDetails{Server: server, Tool: tool, FullOutputPath: fullOutputPath},
		StructuredContent: payload,
	}, nil
}

// ResourceToolsOptions configure [CreateMcpResourceToolDefinitions].
type ResourceToolsOptions struct {
	Exposure extension.McpExposure
	// Servers returns the servers whose resources the tools reach, at call time.
	Servers func() []McpResourceServer
}

// CreateMcpResourceToolDefinitions builds the three resource tools.
func CreateMcpResourceToolDefinitions(options ResourceToolsOptions) []extension.ToolDefinition {
	readOnly := &extension.ToolAnnotations{ReadOnlyHint: new(true)}

	findServer := func(name string) (McpResourceServer, error) {
		servers := options.Servers()
		for _, server := range servers {
			if server.Name() == name {
				return server, nil
			}
		}
		names := make([]string, len(servers))
		for i, server := range servers {
			names[i] = server.Name()
		}
		available := strings.Join(names, ", ")
		if available != "" {
			available = ". Servers with resources: " + available
		}
		return nil, fmt.Errorf(`MCP server "%s" has no resources%s`, name, available)
	}

	requestOptions := func(server McpResourceServer) mcp.RequestOptions {
		return mcp.RequestOptions{TimeoutMs: int(server.Timeout().Milliseconds())}
	}

	// list is one page of one server, or every page of every server.
	list := func(ctx context.Context, params json.RawMessage, key string, page func(context.Context, McpResourceServer, *string, mcp.RequestOptions) ([]any, string, error), all func(context.Context, McpResourceServer, mcp.RequestOptions) ([]any, error), visible func(any) bool) (json.RawMessage, string, error) {
		serverName, err := stringArgument(params, "server")
		if err != nil {
			return nil, "", err
		}
		cursor, err := stringArgument(params, "cursor")
		if err != nil {
			return nil, "", err
		}
		out := orderedjson.New()
		if serverName != "" {
			server, err := findServer(serverName)
			if err != nil {
				return nil, "", err
			}
			var cursorPtr *string
			if cursor != "" {
				cursorPtr = &cursor
			}
			items, next, err := page(ctx, server, cursorPtr, requestOptions(server))
			if err != nil {
				return nil, "", err
			}
			_ = out.SetValue("server", server.Name())
			listedItems := []json.RawMessage{}
			for _, item := range items {
				if visible(item) {
					raw, err := listed(server.Name(), item)
					if err != nil {
						return nil, "", err
					}
					listedItems = append(listedItems, raw)
				}
			}
			_ = out.SetValue(key, listedItems)
			if next != "" {
				_ = out.SetValue("nextCursor", next)
			}
			raw, _ := out.MarshalJSON()
			return raw, serverName, nil
		}
		if cursor != "" {
			return nil, "", errors.New("cursor can only be used when a server is specified")
		}
		servers := slices.Clone(options.Servers())
		slices.SortFunc(servers, func(a, b McpResourceServer) int { return localeCompare(a.Name(), b.Name()) })
		type outcome struct {
			items []any
			err   error
		}
		outcomes := make([]outcome, len(servers))
		var wg sync.WaitGroup
		for i, server := range servers {
			wg.Go(func() {
				items, err := all(ctx, server, requestOptions(server))
				outcomes[i] = outcome{items, err}
			})
		}
		wg.Wait()
		items := []json.RawMessage{}
		type listingError struct {
			Server string `json:"server"`
			Error  string `json:"error"`
		}
		var errs []listingError
		for i, o := range outcomes {
			name := servers[i].Name()
			if o.err != nil {
				errs = append(errs, listingError{name, o.err.Error()})
				continue
			}
			for _, item := range o.items {
				if visible(item) {
					raw, err := listed(name, item)
					if err != nil {
						return nil, "", err
					}
					items = append(items, raw)
				}
			}
		}
		_ = out.SetValue(key, items)
		if len(errs) > 0 {
			_ = out.SetValue("errors", errs)
		}
		raw, _ := out.MarshalJSON()
		return raw, "", nil
	}

	listResources := extension.ToolDefinition{
		Name:         ListMcpResourcesTool,
		Label:        ListMcpResourcesTool,
		Description:  "Lists resources provided by MCP servers. Resources allow servers to share data that provides context to language models, such as files, database schemas, or application-specific information. Prefer resources over web search when possible.",
		Parameters:   json.RawMessage(listParameters),
		OutputSchema: json.RawMessage(listOutputSchema),
		Exposure:     ToToolExposure(options.Exposure),
		Annotations:  readOnly,
		Execute: func(ctx context.Context, _ string, params json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			payload, server, err := list(ctx, params, "resources",
				func(ctx context.Context, server McpResourceServer, cursor *string, opts mcp.RequestOptions) ([]any, string, error) {
					result, err := server.ResourcesPage(ctx, cursor, opts)
					if err != nil {
						return nil, "", err
					}
					items := make([]any, len(result.Resources))
					for i, r := range result.Resources {
						items[i] = r
					}
					return items, result.NextCursor, nil
				},
				func(ctx context.Context, server McpResourceServer, opts mcp.RequestOptions) ([]any, error) {
					resources, err := server.AllResources(ctx, opts)
					items := make([]any, len(resources))
					for i, r := range resources {
						items[i] = r
					}
					return items, err
				},
				func(item any) bool { r := item.(mcp.Resource); return !IsMcpAppResource(r.URI, r.MimeType) })
			if err != nil {
				return nil, err
			}
			return jsonResult(ListMcpResourcesTool, server, payload)
		},
	}

	listTemplates := extension.ToolDefinition{
		Name:         ListMcpResourceTemplatesTool,
		Label:        ListMcpResourceTemplatesTool,
		Description:  "Lists resource templates provided by MCP servers. Parameterized resource templates allow servers to share data that takes parameters and provides context to language models, such as files, database schemas, or application-specific information. Prefer resource templates over web search when possible.",
		Parameters:   json.RawMessage(listParameters),
		OutputSchema: json.RawMessage(listTemplatesOutputSchema),
		Exposure:     ToToolExposure(options.Exposure),
		Annotations:  readOnly,
		Execute: func(ctx context.Context, _ string, params json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			payload, server, err := list(ctx, params, "resourceTemplates",
				func(ctx context.Context, server McpResourceServer, cursor *string, opts mcp.RequestOptions) ([]any, string, error) {
					result, err := server.ResourceTemplatesPage(ctx, cursor, opts)
					if err != nil {
						return nil, "", err
					}
					items := make([]any, len(result.ResourceTemplates))
					for i, r := range result.ResourceTemplates {
						items[i] = r
					}
					return items, result.NextCursor, nil
				},
				func(ctx context.Context, server McpResourceServer, opts mcp.RequestOptions) ([]any, error) {
					templates, err := server.AllResourceTemplates(ctx, opts)
					items := make([]any, len(templates))
					for i, r := range templates {
						items[i] = r
					}
					return items, err
				},
				func(item any) bool {
					r := item.(mcp.ResourceTemplate)
					return !IsMcpAppResource(r.URITemplate, r.MimeType)
				})
			if err != nil {
				return nil, err
			}
			return jsonResult(ListMcpResourceTemplatesTool, server, payload)
		},
	}

	readResource := extension.ToolDefinition{
		Name:         ReadMcpResourceTool,
		Label:        ReadMcpResourceTool,
		Description:  "Read a specific resource from an MCP server given the server name and resource URI.",
		Parameters:   json.RawMessage(readParameters),
		OutputSchema: json.RawMessage(readOutputSchema),
		Exposure:     ToToolExposure(options.Exposure),
		Annotations:  readOnly,
		Execute: func(ctx context.Context, _ string, params json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			serverName, err := stringArgument(params, "server")
			if err != nil {
				return nil, err
			}
			uri, err := stringArgument(params, "uri")
			if err != nil {
				return nil, err
			}
			if serverName == "" {
				return nil, errors.New("server must be provided")
			}
			if uri == "" {
				return nil, errors.New("uri must be provided")
			}
			server, err := findServer(serverName)
			if err != nil {
				return nil, err
			}
			result, err := server.ReadResource(ctx, uri, requestOptions(server))
			if err != nil {
				return nil, err
			}
			// Several contents (for example a directory) are labeled with their URIs.
			var blocks []mcp.ContentBlock
			for _, contents := range result.Contents {
				if len(result.Contents) > 1 {
					blocks = append(blocks, mcp.ContentBlock{Type: "text", Text: contents.URI + ":"})
				}
				resource := contents
				blocks = append(blocks, mcp.ContentBlock{Type: "resource", Resource: &resource})
			}
			converted := ToModelContent(server.Name(), blocks, ConvertMcpResultOptions{})
			if len(converted) == 0 {
				converted = []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("Resource %s is empty.", uri)}}
			}
			content, fullOutputPath := LimitMcpContent(converted, nil)
			contents := make([]json.RawMessage, 0, len(result.Contents))
			for _, c := range result.Contents {
				c.Meta = nil
				data, err := json.Marshal(c)
				if err != nil {
					return nil, err
				}
				contents = append(contents, data)
			}
			structured := orderedjson.New()
			_ = structured.SetValue("server", server.Name())
			_ = structured.SetValue("uri", uri)
			_ = structured.SetValue("contents", contents)
			raw, _ := structured.MarshalJSON()
			return agent.AgentToolResult{
				Content:           content,
				Details:           McpToolDetails{Server: server.Name(), Tool: ReadMcpResourceTool, FullOutputPath: fullOutputPath},
				StructuredContent: raw,
			}, nil
		},
	}
	return []extension.ToolDefinition{listResources, listTemplates, readResource}
}

// collationWeight orders the characters MCP allows in a server name
// (`[A-Za-z0-9_-]`) as ICU's root collation does: punctuation (`_` before `-`),
// then digits, then letters without regard to case.
func collationWeight(c byte) int {
	switch {
	case c == '_':
		return 0
	case c == '-':
		return 1
	case c >= '0' && c <= '9':
		return 2 + int(c-'0')
	case c >= 'a' && c <= 'z':
		return 20 + int(c-'a')
	case c >= 'A' && c <= 'Z':
		return 20 + int(c-'A')
	}
	return 100 + int(c)
}

// localeCompare is JavaScript's `localeCompare` for server names: the primary
// order first, then lower case before upper case.
func localeCompare(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if wa, wb := collationWeight(a[i]), collationWeight(b[i]); wa != wb {
			return wa - wb
		}
	}
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	for i := range len(a) {
		if a[i] != b[i] {
			if a[i] >= 'a' && a[i] <= 'z' {
				return -1
			}
			return 1
		}
	}
	return 0
}
