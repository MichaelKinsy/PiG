package tools

// Ports packages/coding-agent/src/core/tools/render-utils.ts getTextOutput.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// GetTextOutput is a tool result's text for a renderer: its text blocks with ANSI sequences and binary control
// characters removed, followed, when images are not shown, by one fallback line for each image block.
// upstream: packages/coding-agent/src/core/tools/render-utils.ts:41-64
func GetTextOutput(content []ai.ToolResultMessageContent, showImages bool) string {
	var texts, indicators []string
	for _, block := range content {
		switch value := block.(type) {
		case ai.TextContent:
			texts = append(texts, strings.ReplaceAll(SanitizeBinaryOutput(string(StripANSI([]byte(value.Text)))), "\r", ""))
		case ai.ImageContent:
			mimeType := value.MimeType
			if mimeType == "" {
				mimeType = "image/unknown"
			}
			var dimensions *tui.ImageDimensions
			if value.Data != "" && value.MimeType != "" {
				dimensions = tui.GetImageDimensions(value.Data, value.MimeType)
			}
			indicators = append(indicators, tui.ImageFallback(mimeType, dimensions, ""))
		}
	}
	output := strings.Join(texts, "\n")
	if len(indicators) > 0 && (tui.GetCapabilities().Images == "" || !showImages) {
		fallback := strings.Join(indicators, "\n")
		if output == "" {
			return fallback
		}
		return output + "\n" + fallback
	}
	return output
}
