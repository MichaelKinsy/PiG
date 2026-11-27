package mcpext

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/internal/outputfiles"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/coding-agent/src/extensions/mcp/tools.ts.
//
// Adapts MCP tools to pi tool definitions.
//
// Results map onto pi's model-facing content (text and images). Text over 20KB
// keeps its start and end with the middle cut out, like Codex does, and the
// full text is saved to a temp file the model can read. Binary resources other
// than images are saved to temp files too, and resource links name the
// `read_mcp_resource` tool. Codemode scripts receive the whole `CallToolResult`
// without `_meta` (`content` blocks as sent by the server, `structuredContent`,
// `isError`), never truncated: it is the tool's `structuredContent`, and every
// MCP tool declares a `CallToolResult` output schema. MCP errors (`isError`)
// are error results for the model, but scripts still resolve to the result.

// ToToolExposure is the tool exposure of an MCP exposure. `codemode` and
// `deferred` both leave tools out of the codemode description; they differ only
// in which tool the MCP extension activates to reach them.
func ToToolExposure(exposure extension.McpExposure) extension.ToolExposure {
	if exposure == extension.McpExposureCodemode {
		return extension.ToolExposureDeferred
	}
	return extension.ToolExposure(exposure)
}

const (
	// ReadMcpResourceTool is the tool that reads the resources named by resource links.
	ReadMcpResourceTool = extension.ReadMcpResourceTool
)

// McpToolDetails are the details of an MCP tool result.
type McpToolDetails struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	// FullOutputPath is the temp file with the full text output, when the
	// model-facing text was truncated.
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// SaveToTempFile is the [mcp.McpOutputSaver] that saves to a temp file. Results can carry private
// data, so only the user may read the file.
func SaveToTempFile(data []byte, extension string) (string, error) {
	return outputfiles.WriteFile("pi-mcp", extension, data)
}

// McpToolCaller calls tools of one server.
type McpToolCaller interface {
	CallTool(ctx context.Context, name string, args any, options mcp.RequestOptions) (*mcp.CallToolResult, error)
}

func textOf(content []ai.ToolResultMessageContent) string {
	var texts []string
	for _, block := range content {
		if text, ok := block.(ai.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func mcpToLLM(content []mcp.LLMContent) []ai.ToolResultMessageContent {
	out := make([]ai.ToolResultMessageContent, 0, len(content))
	for _, block := range content {
		if block.Type == mcp.LLMContentTypeImage {
			out = append(out, ai.ImageContent{Data: block.Data, MimeType: block.MimeType})
		} else {
			out = append(out, ai.TextContent{Text: block.Text})
		}
	}
	return out
}

// limitMcpContent is [mcp.LimitMcpContent] on model content: text beyond [mcp.McpOutputMaxBytes] is cut in the middle, and saveOutput saves the full
// text. It returns the content as it is when it is within the limit.
func limitMcpContent(content []ai.ToolResultMessageContent, saveOutput mcp.McpOutputSaver) ([]ai.ToolResultMessageContent, string) {
	llm := make([]mcp.LLMContent, 0, len(content))
	for _, block := range content {
		switch block := block.(type) {
		case ai.TextContent:
			llm = append(llm, mcp.LLMContent{Type: mcp.LLMContentTypeText, Text: block.Text})
		case ai.ImageContent:
			llm = append(llm, mcp.LLMContent{Type: mcp.LLMContentTypeImage, Data: block.Data, MimeType: block.MimeType})
		}
	}
	limited := mcp.LimitMcpContent(llm, saveOutput)
	if !limited.Truncated {
		return content, ""
	}
	return mcpToLLM(limited.Content), limited.FullOutputPath
}

// ConvertMcpResultOptions configure [ConvertMcpResult].
type ConvertMcpResultOptions struct {
	// SaveOutput saves truncated text and binary resources. Default: a temp file.
	SaveOutput mcp.McpOutputSaver
	// ReadableResources says whether the server's resources can be read with
	// `read_mcp_resource`, which resource links then name.
	ReadableResources bool
}

// scriptResultOf is the result without `_meta`, as codemode scripts receive it.
func scriptResultOf(result *mcp.CallToolResult) (json.RawMessage, error) {
	raw := result.Raw
	if raw == nil {
		normalized := *result
		if normalized.Content == nil {
			normalized.Content = []mcp.ContentBlock{}
		}
		data, err := json.Marshal(normalized)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	obj, err := orderedjson.Parse(raw)
	if err != nil {
		return nil, err
	}
	obj.Delete("_meta")
	return obj.MarshalJSON()
}

// ConvertMcpResult converts an MCP result. `isError` results become error
// results that keep the structured result.
func ConvertMcpResult(server, tool string, result *mcp.CallToolResult, options ConvertMcpResultOptions) (agent.AgentToolResult, error) {
	saveOutput := options.SaveOutput
	if saveOutput == nil {
		saveOutput = SaveToTempFile
	}
	modelOptions := mcp.McpModelContentOptions{SaveOutput: saveOutput}
	if options.ReadableResources {
		modelOptions.ReadResourceTool = ReadMcpResourceTool
	}
	converted := mcpToLLM(mcp.ToMcpModelContent(server, *result, modelOptions))
	isError := result.IsError != nil && *result.IsError
	if isError && textOf(converted) == "" {
		converted = append(converted, ai.TextContent{Text: fmt.Sprintf("MCP tool %s/%s returned an error", server, tool)})
	}
	content, fullOutputPath := limitMcpContent(converted, saveOutput)
	structured, err := scriptResultOf(result)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	return agent.AgentToolResult{
		Content:           content,
		Details:           McpToolDetails{Server: server, Tool: tool, FullOutputPath: fullOutputPath},
		StructuredContent: structured,
		IsError:           isError,
	}, nil
}

// CreateMcpResultSchema builds the output schema of an MCP tool call result.
//
// Deprecated: Use [mcp.CreateMcpResultSchema] instead. Scheduled for removal in 0.9.0.
func CreateMcpResultSchema(structuredContentSchema json.RawMessage) json.RawMessage {
	return mcp.CreateMcpResultSchema(structuredContentSchema)
}

// ToModelContent returns the model-facing content of server's content blocks before the output limit.
//
// Deprecated: Use [mcp.ToMcpModelContent] instead. Scheduled for removal in 0.9.0.
func ToModelContent(server string, blocks []mcp.ContentBlock, options ConvertMcpResultOptions) []ai.ToolResultMessageContent {
	modelOptions := mcp.McpModelContentOptions{SaveOutput: options.SaveOutput}
	if options.ReadableResources {
		modelOptions.ReadResourceTool = ReadMcpResourceTool
	}
	return mcpToLLM(mcp.ToMcpModelContent(server, mcp.CallToolResult{Content: blocks}, modelOptions))
}

// toToolAnnotations are the boolean hints of an MCP tool's annotations, or nil
// when it has none.
func toToolAnnotations(tool mcp.Tool) *extension.ToolAnnotations {
	if tool.Annotations == nil {
		return nil
	}
	a := &extension.ToolAnnotations{
		ReadOnlyHint: tool.Annotations.ReadOnlyHint, DestructiveHint: tool.Annotations.DestructiveHint,
		IdempotentHint: tool.Annotations.IdempotentHint, OpenWorldHint: tool.Annotations.OpenWorldHint,
	}
	if a.ReadOnlyHint == nil && a.DestructiveHint == nil && a.IdempotentHint == nil && a.OpenWorldHint == nil {
		return nil
	}
	return a
}

// McpToolOptions configure [CreateMcpToolDefinition].
type McpToolOptions struct {
	Server    string
	Tool      mcp.Tool
	Name      string
	Exposure  extension.McpExposure
	Namespace extension.ToolNamespace
	Timeout   int // milliseconds
	GetClient func(ctx context.Context) (McpToolCaller, error)
	// Lane orders the calls to the server; nil leaves them unordered.
	Lane *jsloop.Lane
	// ReadableResources says whether `read_mcp_resource` can read the server's resources.
	ReadableResources func() bool
	// Renderers draws the call and the result in the terminal; nil leaves the tool without renderers (headless).
	Renderers func(label string) *extension.ToolRenderers
}

// CreateMcpToolDefinition adapts one MCP tool to a tool definition.
func CreateMcpToolDefinition(options McpToolOptions) extension.ToolDefinition {
	server, tool := options.Server, options.Tool
	// `tool.title ?? tool.annotations?.title`: only a missing title falls back to the annotation's.
	title := tool.Title
	if title == "" && !tool.HasTitle && tool.Annotations != nil {
		title = tool.Annotations.Title
	}
	description := strings.TrimFunc(tool.Description, isJSWhitespace)
	if description == "" {
		description = title
	}
	if description == "" {
		description = fmt.Sprintf("MCP tool %s from server %s", tool.Name, server)
	}
	namespace := options.Namespace
	var renderCall extension.ToolRenderCallFunc
	var renderResult extension.ToolRenderResultFunc
	if options.Renderers != nil {
		if renderers := options.Renderers(server + "/" + tool.Name); renderers != nil {
			renderCall, renderResult = renderers.RenderCall, renderers.RenderResult
		}
	}
	return extension.ToolDefinition{
		RenderCall:   renderCall,
		RenderResult: renderResult,
		Name:         options.Name,
		Label:        server + "/" + tool.Name,
		Description:  description,
		Parameters:   mcp.NormalizeMcpInputSchema(tool.InputSchema),
		OutputSchema: mcp.CreateMcpResultSchema(tool.OutputSchema),
		Exposure:     ToToolExposure(options.Exposure),
		Namespace:    &namespace,
		Annotations:  toToolAnnotations(tool),
		ReserveCallOrder: func(json.RawMessage) *extension.CallOrder {
			if options.Lane == nil {
				return nil
			}
			place := options.Lane.Reserve()
			return &extension.CallOrder{Wait: place.Wait, Release: place.Release}
		},
		Execute: func(ctx context.Context, _ string, params json.RawMessage, onUpdate extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			requestOptions := mcp.RequestOptions{TimeoutMs: options.Timeout}
			if order, ok := extension.CallOrderFromContext(ctx); ok {
				order.Wait()
				defer order.Release()
				requestOptions.OnIssued = order.Release
			}
			caller, err := options.GetClient(ctx)
			if err != nil {
				return extension.AgentToolResult{}, err
			}
			args := params
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			requestOptions.OnProgress = func(progress mcp.ProgressNotification) {
				if onUpdate == nil {
					return
				}
				text := progress.Message
				if text == "" {
					total := ""
					if progress.Total != nil {
						total = "/" + formatJSNumber(*progress.Total)
					}
					text = "Progress " + formatJSNumber(progress.Progress) + total
				}
				onUpdate(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: McpToolDetails{Server: server, Tool: tool.Name}})
			}
			result, err := caller.CallTool(ctx, tool.Name, args, requestOptions)
			if err != nil {
				return extension.AgentToolResult{}, err
			}
			readable := false
			if options.ReadableResources != nil {
				readable = options.ReadableResources()
			}
			return ConvertMcpResult(server, tool.Name, result, ConvertMcpResultOptions{ReadableResources: readable})
		},
	}
}

// formatJSNumber is String(number) for the values progress notifications carry.
func formatJSNumber(n float64) string {
	return jsnumber.String(n)
}
