package ai

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/ai/src/utils/text.ts

// OwnedSlice is `text.slice(start, end)` as a string of its own (Pi ownedSlice, utils/text.ts:48-50). Go's substring shares its
// source's backing array, so an excerpt kept from a large text (a 500-unit error message cut from a megabyte response body)
// would keep the whole text alive; the copy keeps just the excerpt. Offsets are String.slice's UTF-16 unit offsets; an omitted
// end is the text's length.
func OwnedSlice(text string, start int, end ...int) string {
	return jsstring.OwnedSlice(text, start, end...)
}

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
