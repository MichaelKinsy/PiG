package codingagent

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// ToolResultEventContent preserves text and images at the extension boundary.
func ToolResultEventContent(result agent.AgentToolResult) []any {
	content := make([]any, 0, len(result.Content))
	for _, block := range result.Content {
		switch value := block.(type) {
		case ai.TextContent:
			text := map[string]any{"type": "text", "text": value.Text}
			if value.TextSignature != "" {
				text["textSignature"] = value.TextSignature
			}
			content = append(content, text)
		case ai.ImageContent:
			content = append(content, map[string]any{"type": "image", "data": value.Data, "mimeType": value.MimeType})
		}
	}
	return content
}

// ToolResultEventOverride decodes the runner's complete, chained content without coalescing text or moving images. An empty array clears the prior content.
func ToolResultEventOverride(result *extension.ToolResultEventResult) agent.AfterToolCallResult {
	content := make([]ai.ToolResultMessageContent, 0, len(result.Content))
	for _, block := range result.Content {
		switch value := block.(type) {
		case string:
			content = append(content, ai.TextContent{Text: value})
		case ai.TextContent:
			content = append(content, value)
		case ai.ImageContent:
			content = append(content, value)
		default:
			// Upstream keeps the handler's blocks as they are, so a text or
			// image block whose fields are not strings still reaches the model;
			// the fields are read with JavaScript string coercion. A value with
			// no JSON form has no type, so like an unknown block type it
			// carries nothing the tool result can hold.
			fields := toolResultBlockFields(block)
			switch fields["type"] {
			case "text":
				signature, _ := fields["textSignature"].(string)
				content = append(content, ai.TextContent{Text: jsStringValue(fields["text"]), TextSignature: signature})
			case "image":
				content = append(content, ai.ImageContent{Data: jsStringValue(fields["data"]), MimeType: jsStringValue(fields["mimeType"])})
			}
		}
	}
	// upstream: agent-session.ts:672-678 returns the chained structuredContent with the content; the runner drops it when a handler replaced the content alone (runner.ts:1187).
	return agent.AfterToolCallResult{Content: content, Details: result.Details, StructuredContent: result.StructuredContent, IsError: result.IsError, Usage: toolResultUsage(result.Usage)}
}

// toolResultUsage decodes a handler's usage override; nil keeps the tool's own.
func toolResultUsage(value any) *ai.Usage {
	switch usage := value.(type) {
	case nil:
		return nil
	case *ai.Usage:
		return usage
	case ai.Usage:
		return &usage
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var usage ai.Usage
	if json.Unmarshal(raw, &usage) != nil {
		return nil
	}
	return &usage
}

// toolResultBlockFields returns a content block's JSON object fields. A value
// with no JSON object form has no type, which the caller treats like an
// unknown block type.
func toolResultBlockFields(block any) map[string]any {
	raw, err := json.Marshal(block)
	if err != nil {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	return fields
}

// jsStringValue mirrors JavaScript String(value) for a decoded JSON value;
// an absent field is undefined, which upstream's template literals print as
// "undefined".
func jsStringValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "undefined"
	case string:
		return v
	case float64:
		return tools.FormatJSNumber(v)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if item != nil {
				parts[i] = jsStringValue(item)
			}
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}
