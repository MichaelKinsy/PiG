package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/outputfiles"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
)

// Ports apps/p2/plugins/mcp/tools.ts: MCP tools as Durable tools, exposed as pi exposes them by default: one tool per tool of
// the session's servers, named as pi names them (`mcp__<server>__<tool>`), callable only from code mode scripts (or loaded
// through `tool_search` while code mode is off), calling the server's instance where the calling conversation works; and the
// `mcp_servers` prompt section, through which the model learns of the servers.

// ServerTool is one tool of one server.
type ServerTool struct {
	Server string
	Tool   pimcp.Tool
}

// McpReach is how the model reaches MCP tools, as pi's exposures: `codemode` (pi's default) from code mode scripts, which
// find them with `searchTools()`; `tool_search` (pi's `deferred`, its fallback without code mode) by loading them with
// `tool_search`, then calling them directly.
type McpReach string

// The ways the model reaches MCP tools.
const (
	ReachCodemode   McpReach = "codemode"
	ReachToolSearch McpReach = "tool_search"
)

// McpOffer is what the MCP extension offers: the session's servers by name, those waiting for the owner to sign in, their
// tools, and how the model reaches them.
type McpOffer struct {
	Servers []string
	SignIn  []string
	Tools   []ServerTool
	Reach   McpReach
}

// McpCallRequest is a call of Server's Tool for conversation ConversationID (its documents read through Read).
type McpCallRequest struct {
	ConversationID durable.ConversationId
	Read           durable.DocumentReader
	Server         string
	Tool           string
	Args           map[string]any
}

// McpCall calls an MCP tool.
type McpCall func(ctx context.Context, request McpCallRequest) (*pimcp.CallToolResult, error)

// ServerTools is the tools of servers by server and tool name; a server with instances at several locations lists a tool once.
func ServerTools(servers []Instance) []ServerTool {
	seen := map[[2]string]bool{}
	var tools []ServerTool
	for _, instance := range servers {
		for _, tool := range instance.Tools {
			key := [2]string{instance.Server, tool.Name}
			if !seen[key] {
				seen[key] = true
				tools = append(tools, ServerTool{Server: instance.Server, Tool: tool})
			}
		}
	}
	slices.SortStableFunc(tools, func(a, b ServerTool) int {
		if order := strings.Compare(a.Server, b.Server); order != 0 {
			return order
		}
		return strings.Compare(a.Tool.Name, b.Tool.Name)
	})
	return tools
}

// ServerNames is the servers of instances by name, each once (a server may have instances at several locations).
func ServerNames(instances []Instance) []string {
	names := []string{}
	for _, instance := range instances {
		if !slices.Contains(names, instance.Server) {
			names = append(names, instance.Server)
		}
	}
	slices.Sort(names)
	return names
}

// SignInNames is the servers of instances waiting for a sign-in, by name.
func SignInNames(instances []Instance) []string {
	var waiting []Instance
	for _, instance := range instances {
		if instance.Status == "sign-in" {
			waiting = append(waiting, instance)
		}
	}
	return ServerNames(waiting)
}

// McpServersSection is the section listing the servers whose tools are not declared to the model, as pi's.
const McpServersSection = "mcp_servers"

// maxServersSectionChars is the characters of the whole section, as in pi: when the server lines do not fit, the last
// servers are left out and counted in a closing line.
const maxServersSectionChars = 4096

// CreateMcpExtension is the Durable extension offering offer's tools, each calling through call, and the `mcp_servers`
// section. As in pi (and Codex), every tool whose name sanitizes to one another tool's also gets pi's hash suffix, so no
// tool's name depends on the order of the list and a new tool renames only those it collides with.
func CreateMcpExtension(offer McpOffer, call McpCall) *p2runtime.P2Extension {
	plain := map[string]int{}
	for _, entry := range offer.Tools {
		plain[pimcp.CreateMcpToolName(entry.Server, entry.Tool.Name, nil)]++
	}
	assigned := map[string]bool{}
	extension := &durable.Extension{Name: contract.McpExtension, Sections: []*durable.PromptSection{}}
	tools := make([]p2runtime.P2Tool, 0, len(offer.Tools))
	for _, entry := range offer.Tools {
		name := pimcp.CreateMcpToolName(entry.Server, entry.Tool.Name, func(candidate string) bool { return plain[candidate] > 1 || assigned[candidate] })
		assigned[name] = true
		tools = append(tools, toolRegistration(name, entry, offer.Reach, call))
	}
	if listing, ok := serversSection(offer.Servers, offer.SignIn, offer.Reach); ok {
		extension.Sections = append(extension.Sections, &durable.PromptSection{
			Key:    McpServersSection,
			Render: func(context.Context, durable.PromptInput) (*string, error) { return &listing, nil },
		})
	}
	return &p2runtime.P2Extension{Extension: extension, Tools: tools}
}

func jsLength(text string) int { return len(utf16.Encode([]rune(text))) }

// serversSection is the `mcp_servers` section, as pi renders it for servers without a description: each server's namespace
// and how its tools are reached; for a server waiting for a sign-in, the link the user signs in with. Absent without servers.
func serversSection(servers, signIn []string, reach McpReach) (string, bool) {
	if len(servers) == 0 {
		return "", false
	}
	intro := "MCP servers whose tools are not declared to you. Load the tools of `tool_search` servers with `tool_search`."
	find := "tool_search"
	if reach == ReachCodemode {
		intro = "MCP servers whose tools are not declared to you. Call the tools of `codemode` servers from codemode scripts."
		find = "searchTools()"
	}
	lines := make([]string, len(servers))
	for i, server := range servers {
		if slices.Contains(signIn, server) {
			lines[i] = fmt.Sprintf("- %s (%s; needs a sign-in, no tools yet: the user signs in from the MCP panel in p2's sidebar or at %s on p2)", pimcp.McpNamespace(server), reach, contract.SignInLink(server))
		} else {
			lines[i] = fmt.Sprintf("- %s (%s)", pimcp.McpNamespace(server), reach)
		}
	}
	render := func(kept int) string {
		parts := append([]string{intro}, lines[:kept]...)
		if omitted := len(lines) - kept; omitted > 0 {
			plural := "s"
			if omitted == 1 {
				plural = ""
			}
			parts = append(parts, fmt.Sprintf("- … %d more server%s; find their tools with %s", omitted, plural, find))
		}
		return strings.Join(parts, "\n")
	}
	kept := len(lines)
	for kept > 0 && jsLength(render(kept)) > maxServersSectionChars {
		kept--
	}
	return render(kept), true
}

// toolRegistration is one MCP tool as a Durable tool (not replay-safe: an interrupted call is not repeated). Deferred either
// way, so the `codemode` section leaves it out; for `codemode` only other tools (scripts) may call it, so `tool_search` never
// offers it to the model either. Only turns the owner started may call it: the owner's accounts are behind the servers.
func toolRegistration(name string, entry ServerTool, reach McpReach, call McpCall) p2runtime.P2Tool {
	tool := entry.Tool
	title := tool.Title
	if title == "" && !tool.HasTitle && tool.Annotations != nil {
		title = tool.Annotations.Title
	}
	description := jsstring.Trim(tool.Description)
	if description == "" {
		description = title
	}
	if description == "" {
		description = fmt.Sprintf("MCP tool %s from server %s", tool.Name, entry.Server)
	}
	registration := &durable.ToolRegistration{
		ToolSchema: toolSchemaOf(name, description, pimcp.NormalizeMcpInputSchema(tool.InputSchema)),
		Deferred:   true,
	}
	if reach == ReachCodemode {
		registration.Callers = []durable.ToolCaller{durable.CallerTools}
	}
	declaresOutput := len(tool.OutputSchema) > 0
	if declaresOutput {
		var schema map[string]any
		if err := json.Unmarshal(tool.OutputSchema, &schema); err == nil {
			registration.StructuredOutputSchema = schema
		}
	}
	registration.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		// Durable checked args against Parameters, an object schema.
		arguments, _ := args.(map[string]any)
		result, err := call(ctx, McpCallRequest{ConversationID: api.ConversationId(), Read: api, Server: entry.Server, Tool: tool.Name, Args: arguments})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		content := mcpToLLM(pimcp.ToLLMContent(*result))
		isError := result.IsError != nil && *result.IsError
		if isError && !hasText(content) {
			content = append(content, ai.TextContent{Text: fmt.Sprintf("MCP tool %s/%s returned an error", entry.Server, tool.Name)})
		}
		limited := pimcp.LimitMcpContent(toLLMContent(content), saveOutput)
		if limited.Truncated {
			content = mcpToLLM(limited.Content)
		}
		out := durable.ToolExecutionResult{
			Output: content, IsError: &isError, HasDetails: true,
			Details: map[string]any{"server": entry.Server, "tool": tool.Name},
		}
		// Structured output only for a tool that declares its schema (Durable checks it), error results too.
		if declaresOutput && len(result.StructuredContent) > 0 {
			var structured any
			if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			out.StructuredOutput, out.HasStructuredOutput = structured, true
		}
		return out, nil
	}
	return p2runtime.P2Tool{ToolRegistration: registration, ToolAccess: p2runtime.ToolAccess{Origin: "owner"}}
}

// toolSchemaOf is a tool's declaration, its parameters' declaration order kept.
func toolSchemaOf(name, description string, parameters json.RawMessage) ai.ToolSchema {
	declared, err := json.Marshal(map[string]any{"name": name, "description": description})
	if err != nil {
		panic(err)
	}
	// {"name":…,"description":…} with the parameters member added as written.
	text := string(declared[:len(declared)-1]) + `,"parameters":` + string(parameters) + "}"
	var schema ai.ToolSchema
	if err := json.Unmarshal([]byte(text), &schema); err != nil {
		panic(err)
	}
	return schema
}

func saveOutput(data []byte, extension string) (string, error) {
	return outputfiles.WriteFile("pi-mcp", extension, data)
}

func hasText(content []ai.ToolResultMessageContent) bool {
	for _, block := range content {
		if text, ok := block.(ai.TextContent); ok && jsstring.Trim(text.Text) != "" {
			return true
		}
	}
	return false
}

func mcpToLLM(content []pimcp.LLMContent) []ai.ToolResultMessageContent {
	out := make([]ai.ToolResultMessageContent, 0, len(content))
	for _, block := range content {
		if block.Type == pimcp.LLMContentTypeImage {
			out = append(out, ai.ImageContent{Data: block.Data, MimeType: block.MimeType})
		} else {
			out = append(out, ai.TextContent{Text: block.Text})
		}
	}
	return out
}

func toLLMContent(content []ai.ToolResultMessageContent) []pimcp.LLMContent {
	out := make([]pimcp.LLMContent, 0, len(content))
	for _, block := range content {
		switch block := block.(type) {
		case ai.TextContent:
			out = append(out, pimcp.LLMContent{Type: pimcp.LLMContentTypeText, Text: block.Text})
		case ai.ImageContent:
			out = append(out, pimcp.LLMContent{Type: pimcp.LLMContentTypeImage, Data: block.Data, MimeType: block.MimeType})
		}
	}
	return out
}
