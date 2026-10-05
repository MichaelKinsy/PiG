package mcpext

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/outputfiles"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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
	// maxToolNameLength: provider tool names are limited to 64 characters of `[A-Za-z0-9_-]`.
	// upstream: packages/coding-agent/src/extensions/mcp/tools.ts:MAX_TOOL_NAME_LENGTH
	maxToolNameLength = 64
	// McpOutputMaxBytes: model-facing text of an MCP result beyond this is cut in the middle.
	// upstream: packages/coding-agent/src/extensions/mcp/tools.ts:MCP_OUTPUT_MAX_BYTES
	McpOutputMaxBytes = 20 * 1024
	// ReadMcpResourceTool is the tool that reads the resources named by resource links.
	ReadMcpResourceTool = "read_mcp_resource"
)

// McpToolDetails are the details of an MCP tool result.
type McpToolDetails struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	// FullOutputPath is the temp file with the full text output, when the
	// model-facing text was truncated.
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// McpOutputSaver saves the full text of a truncated result, or a binary
// resource, and returns the file path. extension includes the dot, for example
// `.txt`.
type McpOutputSaver func(data []byte, extension string) (string, error)

// SaveToTempFile is the default [McpOutputSaver]. Results can carry private
// data, so only the user may read the file.
func SaveToTempFile(data []byte, extension string) (string, error) {
	return outputfiles.WriteFile("pi-mcp", extension, data)
}

// McpToolCaller calls tools of one server.
type McpToolCaller interface {
	CallTool(ctx context.Context, name string, args any, options mcp.RequestOptions) (*mcp.CallToolResult, error)
}

var toolNameUnsafe = lazyregexp.New(`[^A-Za-z0-9_]`)

// sanitizeToolName replaces every character outside `[A-Za-z0-9_]` with an
// underscore per UTF-16 code unit, as the JavaScript regular expression does.
func sanitizeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x80 && !toolNameUnsafe.MatchString(string(r)) {
			b.WriteRune(r)
			continue
		}
		b.WriteString(strings.Repeat("_", utf16.RuneLen(r)))
	}
	return b.String()
}

// CreateMcpToolName is `mcp__<server>__<tool>`, sanitized and shortened with a
// hash suffix when too long. Like Codex, everything but `[A-Za-z0-9_]` becomes
// `_`, so the name is also the identifier codemode scripts call it by. isTaken
// reports names used by a different MCP tool: sanitizing can map two tools to
// one name (`a-b` and `a_b`), which then get the hash suffix.
func CreateMcpToolName(server, tool string, isTaken func(string) bool) string {
	name := sanitizeToolName("mcp__" + server + "__" + tool)
	if len(name) <= maxToolNameLength && (isTaken == nil || !isTaken(name)) {
		return name
	}
	digest := sha256.Sum256([]byte(server + "\x00" + tool))
	hash := hex.EncodeToString(digest[:])[:8]
	return name[:min(len(name), maxToolNameLength-len(hash)-1)] + "_" + hash
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

// CreateMcpResultSchema is the output schema of every MCP tool: the
// `CallToolResult` scripts receive, with the tool's own output schema as
// `structuredContent`. Codemode detects this shape to render
// `CallToolResult<T>` declarations.
func CreateMcpResultSchema(structuredContentSchema json.RawMessage) json.RawMessage {
	properties := orderedjson.New()
	_ = properties.SetValue("content", json.RawMessage(`{"type":"array","items":{"type":"object"}}`))
	if len(structuredContentSchema) > 0 {
		properties.Set("structuredContent", structuredContentSchema)
	}
	_ = properties.SetValue("isError", json.RawMessage(`{"type":"boolean"}`))
	_ = properties.SetValue("_meta", json.RawMessage(`{"type":"object"}`))
	schema := orderedjson.New()
	_ = schema.SetValue("type", "object")
	propertiesRaw, _ := properties.MarshalJSON()
	schema.Set("properties", propertiesRaw)
	_ = schema.SetValue("required", []string{"content"})
	raw, _ := schema.MarshalJSON()
	return raw
}

func mcpToLLM(content []mcp.LLMContent) []ai.ToolResultMessageContent {
	out := make([]ai.ToolResultMessageContent, 0, len(content))
	for _, block := range content {
		if block.Type == "image" {
			out = append(out, ai.ImageContent{Data: block.Data, MimeType: block.MimeType})
		} else {
			out = append(out, ai.TextContent{Text: block.Text})
		}
	}
	return out
}

// LimitMcpContent keeps model-facing text within [McpOutputMaxBytes]. Longer
// text becomes one text block in Codex's truncation format, followed by the
// path of the file with the full text; images follow it.
func LimitMcpContent(content []ai.ToolResultMessageContent, saveOutput McpOutputSaver) ([]ai.ToolResultMessageContent, string) {
	if saveOutput == nil {
		saveOutput = SaveToTempFile
	}
	combined := textOf(content)
	truncation := tools.TruncateMiddle(combined, McpOutputMaxBytes)
	if !truncation.Truncated {
		return content, ""
	}
	var fullOutputPath, where string
	if path, err := saveOutput([]byte(combined), ".txt"); err == nil {
		fullOutputPath = path
		where = "[Full output: " + path + " (read it with offset/limit)]"
	} else {
		where = "[Could not save the full output: " + err.Error() + "]"
	}
	tokens := (truncation.TotalBytes + 3) / 4
	text := fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: %d\n\n%s\n\n%s", tokens, truncation.TotalLines, truncation.Content, where)
	limited := []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
	for _, block := range content {
		if _, ok := block.(ai.ImageContent); ok {
			limited = append(limited, block)
		}
	}
	return limited, fullOutputPath
}

// ConvertMcpResultOptions configure [ConvertMcpResult].
type ConvertMcpResultOptions struct {
	// SaveOutput saves truncated text and binary resources. Default: a temp file.
	SaveOutput McpOutputSaver
	// ReadableResources says whether the server's resources can be read with
	// `read_mcp_resource`, which resource links then name.
	ReadableResources bool
}

var fileExtension = lazyregexp.New(`\.[A-Za-z0-9]{1,8}$`)

// extensionOf is the file extension for a saved binary resource: the one its
// URI ends in, else `.bin`.
func extensionOf(uri string) string {
	path := uri
	if u, err := url.Parse(uri); err == nil && u.Scheme != "" {
		// The WHATWG pathname of a URL without a hierarchical part
		// (`note:readme.pdf`) is its opaque path.
		path = u.EscapedPath()
		if u.Opaque != "" {
			path = u.Opaque
		}
	}
	if match := fileExtension.FindString(path); match != "" {
		return match
	}
	return ".bin"
}

// isTextMimeType: blobs of these types are shown as text.
func isTextMimeType(mimeType string) bool {
	if mimeType == "" {
		return false
	}
	kind, _, _ := strings.Cut(mimeType, ";")
	kind = strings.ToLower(strings.TrimSpace(kind))
	return strings.HasPrefix(kind, "text/") || kind == "application/json" || strings.HasSuffix(kind, "+json") || strings.HasSuffix(kind, "+xml")
}

// blockToContent is the model-facing content of one block of server's result.
func blockToContent(server string, block mcp.ContentBlock, options ConvertMcpResultOptions) []ai.ToolResultMessageContent {
	if block.Type == "resource_link" {
		var details []string
		if block.MimeType != "" {
			details = append(details, block.MimeType)
		}
		if block.Size != nil {
			details = append(details, tools.FormatSize(int(*block.Size)))
		}
		read := ""
		if options.ReadableResources {
			read = fmt.Sprintf(`. Read it with %s (server "%s")`, ReadMcpResourceTool, server)
		}
		description := ""
		if block.Description != "" {
			description = ": " + block.Description
		}
		title := block.Title
		if title == "" {
			title = block.Name
		}
		suffix := ""
		if len(details) > 0 {
			suffix = " (" + strings.Join(details, ", ") + ")"
		}
		return []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf(`[Resource %s "%s"%s%s%s]`, block.URI, title, suffix, description, read)}}
	}
	if block.Type == "resource" && block.Resource != nil && block.Resource.Blob != nil && !strings.HasPrefix(block.Resource.MimeType, "image/") {
		resource := block.Resource
		data, err := base64Decode(*resource.Blob)
		if err != nil {
			data = nil
		}
		if isTextMimeType(resource.MimeType) {
			return []ai.ToolResultMessageContent{ai.TextContent{Text: strings.ToValidUTF8(string(data), "\uFFFD")}}
		}
		mimeType := resource.MimeType
		if mimeType == "" {
			mimeType = "unknown type"
		}
		kind := mimeType + ", " + tools.FormatSize(len(data))
		save := options.SaveOutput
		if save == nil {
			save = SaveToTempFile
		}
		path, err := save(data, extensionOf(resource.URI))
		if err != nil {
			return []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("[Binary resource %s (%s) could not be saved: %s]", resource.URI, kind, err)}}
		}
		return []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("[Binary resource %s (%s) saved to %s]", resource.URI, kind, path)}}
	}
	return mcpToLLM(mcp.ToLLMContent(mcp.CallToolResult{Content: []mcp.ContentBlock{block}}))
}

// ToModelContent is the model-facing content of server's content blocks,
// before the output limit.
func ToModelContent(server string, blocks []mcp.ContentBlock, options ConvertMcpResultOptions) []ai.ToolResultMessageContent {
	var out []ai.ToolResultMessageContent
	for _, block := range blocks {
		out = append(out, blockToContent(server, block, options)...)
	}
	return out
}

// scriptResultOf is the result without `_meta`, as codemode scripts receive it.
func scriptResultOf(result *mcp.CallToolResult) (json.RawMessage, error) {
	raw := result.Raw
	if raw == nil {
		data, err := json.Marshal(result)
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
	// Without content blocks, ToLLMContent falls back to the structured content as JSON.
	var converted []ai.ToolResultMessageContent
	if len(result.Content) > 0 {
		converted = ToModelContent(server, result.Content, options)
	} else {
		converted = mcpToLLM(mcp.ToLLMContent(*result))
	}
	isError := result.IsError != nil && *result.IsError
	if isError && textOf(converted) == "" {
		converted = append(converted, ai.TextContent{Text: fmt.Sprintf("MCP tool %s/%s returned an error", server, tool)})
	}
	content, fullOutputPath := LimitMcpContent(converted, options.SaveOutput)
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

// toParameters: tool input schemas must be objects. MCP servers may omit
// `type`, and some providers reject object schemas without `properties`.
func toParameters(schema json.RawMessage) json.RawMessage {
	obj, err := orderedjson.Parse(schema)
	if err != nil {
		obj = orderedjson.New()
	}
	if raw, ok := obj.Get("type"); !ok || string(raw) == "null" {
		_ = obj.SetValue("type", "object")
	}
	if !obj.Has("properties") {
		obj.Set("properties", json.RawMessage("{}"))
	}
	raw, _ := obj.MarshalJSON()
	return raw
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
	Lane *extension.CallLane
	// ReadableResources says whether `read_mcp_resource` can read the server's resources.
	ReadableResources func() bool
}

// CreateMcpToolDefinition adapts one MCP tool to a tool definition.
func CreateMcpToolDefinition(options McpToolOptions) extension.ToolDefinition {
	server, tool := options.Server, options.Tool
	title := tool.Title
	if title == "" && tool.Annotations != nil {
		title = tool.Annotations.Title
	}
	description := strings.TrimSpace(tool.Description)
	if description == "" {
		description = title
	}
	if description == "" {
		description = fmt.Sprintf("MCP tool %s from server %s", tool.Name, server)
	}
	namespace := options.Namespace
	return extension.ToolDefinition{
		Name:         options.Name,
		Label:        server + "/" + tool.Name,
		Description:  description,
		Parameters:   toParameters(tool.InputSchema),
		OutputSchema: CreateMcpResultSchema(tool.OutputSchema),
		Exposure:     ToToolExposure(options.Exposure),
		Namespace:    &namespace,
		Annotations:  toToolAnnotations(tool),
		RenderCall:   renderCall(server + "/" + tool.Name),
		ReserveCallOrder: func(json.RawMessage) *extension.CallOrder {
			if options.Lane == nil {
				return nil
			}
			return options.Lane.Reserve()
		},
		RenderResult: renderResult,
		Execute: func(ctx context.Context, _ string, params json.RawMessage, onUpdate extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			requestOptions := mcp.RequestOptions{TimeoutMs: options.Timeout}
			if order, ok := extension.CallOrderFromContext(ctx); ok {
				order.Wait()
				defer order.Release()
				requestOptions.OnIssued = order.Release
			}
			caller, err := options.GetClient(ctx)
			if err != nil {
				return nil, err
			}
			args := params
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			requestOptions.OnProgress = func(progress mcp.ProgressNotification) {
				update, ok := onUpdate.(agent.ToolUpdateCallback)
				if !ok || update == nil {
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
				update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: McpToolDetails{Server: server, Tool: tool.Name}})
			}
			result, err := caller.CallTool(ctx, tool.Name, args, requestOptions)
			if err != nil {
				return nil, err
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

// base64Decode is Buffer.from(s, "base64"): it accepts both alphabets, skips
// characters outside them, and does not need padding.
func base64Decode(s string) ([]byte, error) {
	var clean []byte
	for i := range len(s) {
		switch c := s[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/':
			clean = append(clean, c)
		case c == '-':
			clean = append(clean, '+')
		case c == '_':
			clean = append(clean, '/')
		case c == '=':
			return base64.RawStdEncoding.DecodeString(string(trimPartial(clean)))
		}
	}
	return base64.RawStdEncoding.DecodeString(string(trimPartial(clean)))
}

// trimPartial drops a lone trailing sextet, which encodes no byte.
func trimPartial(b []byte) []byte {
	if len(b)%4 == 1 {
		return b[:len(b)-1]
	}
	return b
}
