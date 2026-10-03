package tools

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// getTextOutput (.upstream/v0.99.1/packages/coding-agent/src/core/tools/render-utils.ts:41-64): text blocks lose ANSI
// sequences, binary control characters and carriage returns; images that are not shown become one fallback line each.
func TestGetTextOutput(t *testing.T) {
	image := ai.ImageContent{Data: "AAAA", MimeType: "image/png"}
	for _, tc := range []struct {
		name       string
		content    []ai.ToolResultMessageContent
		showImages bool
		want       string
	}{
		{"none", nil, false, ""},
		{"text blocks join with newline", []ai.ToolResultMessageContent{ai.TextContent{Text: "a"}, ai.TextContent{Text: "b"}}, false, "a\nb"},
		{"sanitized", []ai.ToolResultMessageContent{ai.TextContent{Text: "\x1b[31mred\x1b[0m\r\nbell\x07\tend"}}, false, "red\nbell\tend"},
		{"image indicator after the text", []ai.ToolResultMessageContent{ai.TextContent{Text: "caption"}, image}, false, "caption\n[Image: [image/png]]"},
		{"image indicator alone", []ai.ToolResultMessageContent{image, ai.ImageContent{Data: "", MimeType: ""}}, false, "[Image: [image/png]]\n[Image: [image/unknown]]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := GetTextOutput(tc.content, tc.showImages); got != tc.want {
				t.Fatalf("GetTextOutput = %q, want %q", got, tc.want)
			}
		})
	}
}
