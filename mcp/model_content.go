package mcp

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodebase64"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports packages/mcp/src/model-content.ts (Pi 1acc72acd).
//
// The model-facing content of an MCP result as pi's coding agent shows it: text and images, embedded text resources
// as text, binary resources saved where the model can read them, and resource links that name the tool reading them.

// McpModelContentOptions configures how [ToMcpModelContent] handles what the model cannot read inline.
type McpModelContentOptions struct {
	// SaveOutput saves binary resources other than images where the model can read them. Without it they are left out.
	SaveOutput McpOutputSaver
	// ReadResourceTool is the tool that reads the server's resources, which resource links then name.
	ReadResourceTool string
}

// ToMcpModelContent returns the model-facing content of server's result, before the output limit ([LimitMcpContent]).
// A result without content blocks becomes its structured content as JSON, as in [ToLLMContent].
func ToMcpModelContent(server string, result CallToolResult, options McpModelContentOptions) []LLMContent {
	if len(result.Content) == 0 {
		return ToLLMContent(result)
	}
	out := make([]LLMContent, 0, len(result.Content))
	for _, block := range result.Content {
		out = append(out, blockToModelContent(server, block, options)...)
	}
	return out
}

// blockToModelContent is the model-facing content of one block of server's result.
func blockToModelContent(server string, block ContentBlock, options McpModelContentOptions) []LLMContent {
	if block.Type == ContentResourceLink {
		var details []string
		if block.MimeType != "" {
			details = append(details, block.MimeType)
		}
		if block.Size != nil {
			details = append(details, formatSize(*block.Size))
		}
		read := ""
		if options.ReadResourceTool != "" {
			read = fmt.Sprintf(`. Read it with %s (server "%s")`, options.ReadResourceTool, server)
		}
		description := ""
		if block.Description != "" {
			description = ": " + block.Description
		}
		// `block.title ?? block.name`: only a missing title falls back to the name.
		title := block.Title
		if title == "" && !block.HasTitle {
			title = block.Name
		}
		suffix := ""
		if len(details) > 0 {
			suffix = " (" + strings.Join(details, ", ") + ")"
		}
		return []LLMContent{{Type: LLMContentTypeText, Text: fmt.Sprintf(`[Resource %s "%s"%s%s%s]`, block.URI, title, suffix, description, read)}}
	}
	if block.Type == ContentResource && block.Resource != nil && block.Resource.Blob != nil && !strings.HasPrefix(block.Resource.MimeType, "image/") {
		resource := block.Resource
		data := nodebase64.Decode(*resource.Blob)
		if isTextMimeType(resource.MimeType) {
			return []LLMContent{{Type: LLMContentTypeText, Text: jsstring.FromUTF8(data)}}
		}
		if options.SaveOutput == nil {
			return ToLLMContent(CallToolResult{Content: []ContentBlock{block}})
		}
		mimeType := resource.MimeType
		if mimeType == "" && !resource.HasMimeType {
			mimeType = "unknown type"
		}
		kind := mimeType + ", " + formatSize(float64(len(data)))
		path, err := options.SaveOutput(data, extensionOf(resource.URI))
		if err != nil {
			return []LLMContent{{Type: LLMContentTypeText, Text: fmt.Sprintf("[Binary resource %s (%s) could not be saved: %s]", resource.URI, kind, err)}}
		}
		return []LLMContent{{Type: LLMContentTypeText, Text: fmt.Sprintf("[Binary resource %s (%s) saved to %s]", resource.URI, kind, path)}}
	}
	return ToLLMContent(CallToolResult{Content: []ContentBlock{block}})
}

// formatSize is a byte count as pi's coding agent shows sizes: `512B`, `2.0KB`, `1.5MB`.
func formatSize(bytes float64) string {
	if bytes < 1024 {
		return jsnumber.String(bytes) + "B"
	}
	if bytes < 1024*1024 {
		return jsstring.ToFixed(bytes/1024, 1) + "KB"
	}
	return jsstring.ToFixed(bytes/(1024*1024), 1) + "MB"
}

// isTextMimeType: blobs of these types are shown as text.
func isTextMimeType(mimeType string) bool {
	if mimeType == "" {
		return false
	}
	kind, _, _ := strings.Cut(mimeType, ";")
	kind = strings.ToLower(jsstring.Trim(kind))
	return strings.HasPrefix(kind, "text/") || kind == "application/json" || strings.HasSuffix(kind, "+json") || strings.HasSuffix(kind, "+xml")
}

var fileExtension = lazyregexp.New(`\.[A-Za-z0-9]{1,8}$`)

// extensionOf is the file extension for a saved binary resource: the one its
// URI ends in, else `.bin`.
func extensionOf(uri string) string {
	path := uri
	if u, err := nodeurl.ParseURL(uri); err == nil {
		path = u.Pathname
	}
	if match := fileExtension.FindString(path); match != "" {
		return match
	}
	return ".bin"
}
