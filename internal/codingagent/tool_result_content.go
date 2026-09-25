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
	content := make([]any, 0, 1+len(result.Images))
	if result.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": result.Content})
	}
	for _, image := range result.Images {
		content = append(content, map[string]any{"type": "image", "data": image.Data, "mimeType": image.MimeType})
	}
	return content
}

// ToolResultEventOverride decodes the runner's complete, chained content. An
// empty image list intentionally clears images replaced by a text-only result.
func ToolResultEventOverride(result *extension.ToolResultEventResult) agent.AfterToolCallResult {
	var text strings.Builder
	images := make([]ai.ImageContent, 0)
	for _, block := range result.Content {
		switch value := block.(type) {
		case string:
			text.WriteString(value)
		case ai.TextContent:
			text.WriteString(value.Text)
		case ai.ImageContent:
			images = append(images, value)
		default:
			// Upstream keeps the handler's blocks as they are, so a text or
			// image block whose fields are not strings still reaches the model;
			// the fields are read with JavaScript string coercion. A value with
			// no JSON form has no type, so like an unknown block type it
			// carries nothing the tool result can hold.
			fields := toolResultBlockFields(block)
			switch fields["type"] {
			case "text":
				text.WriteString(jsStringValue(fields["text"]))
			case "image":
				images = append(images, ai.ImageContent{Data: jsStringValue(fields["data"]), MimeType: jsStringValue(fields["mimeType"])})
			}
		}
	}
	content := text.String()
	return agent.AfterToolCallResult{Content: &content, Images: &images, Details: result.Details, IsError: result.IsError, Usage: toolResultUsage(result.Usage)}
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
