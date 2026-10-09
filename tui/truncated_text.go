package tui

// truncated_text.go: width-truncated text component.
//
// Ports upstream packages/tui/src/components/truncated-text.ts.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TruncatedText renders text truncated to fit viewport width.
type TruncatedText struct {
	invalidatable
	Content  string
	PaddingX int
	PaddingY int
}

// NewTruncatedText ports the constructor (text, paddingX = 0, paddingY = 0) of truncated-text.ts:12; Go has no default parameters, so a caller passes 0.
func NewTruncatedText(content string, paddingX, paddingY int) *TruncatedText {
	return &TruncatedText{Content: content, PaddingX: paddingX, PaddingY: paddingY}
}

func (t *TruncatedText) Render(width int) []string {
	var result []string

	emptyLine := strings.Repeat(" ", width)
	for range t.PaddingY {
		result = append(result, emptyLine)
	}

	// pig divergence (D66): Clamp horizontal padding when the fixed padding
	// plus one content cell would exceed the requested width. Upstream keeps
	// the full padding and can emit an over-wide row that stops its TUI.
	paddingX := min(t.PaddingX, max(0, (width-1)/2))
	avail := max(1, width-paddingX*2)
	text := t.Content
	if idx := strings.Index(text, "\n"); idx >= 0 {
		text = text[:idx]
	}
	display := widthx.TruncateToWidth(text, avail, "...", false)

	left := strings.Repeat(" ", paddingX)
	right := strings.Repeat(" ", paddingX)
	line := left + display + right
	if pad := width - widthx.VisibleWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	result = append(result, line)

	for range t.PaddingY {
		result = append(result, emptyLine)
	}
	return result
}
