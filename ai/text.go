package ai

import "strings"

// Ports packages/ai/src/utils/text.ts

// ContentText extracts text blocks in order and joins them with a newline, or the supplied separator. String content passes through unchanged.
func ContentText[T string | UserText | SystemText | []AssistantContentBlock | []ToolResultMessageContent | UserContentBlocks | SystemTextBlocks | ContentBlocks](content T, separator ...string) string {
	sep := "\n"
	if len(separator) != 0 {
		sep = separator[0]
	}
	switch value := any(content).(type) {
	case string:
		return value
	case UserText:
		return string(value)
	case SystemText:
		return string(value)
	case []AssistantContentBlock:
		return contentBlocksText(value, sep)
	case []ToolResultMessageContent:
		return contentBlocksText(value, sep)
	case UserContentBlocks:
		return contentBlocksText(value, sep)
	case SystemTextBlocks:
		return contentBlocksText(value, sep)
	case ContentBlocks:
		return contentBlocksText(value, sep)
	}
	panic("unreachable content type")
}

func contentBlocksText[T interface{ contentType() string }](blocks []T, separator string) string {
	var out strings.Builder
	first := true
	for _, block := range blocks {
		text, ok := any(block).(TextContent)
		if !ok {
			continue
		}
		if !first {
			out.WriteString(separator)
		}
		out.WriteString(text.Text)
		first = false
	}
	return out.String()
}
