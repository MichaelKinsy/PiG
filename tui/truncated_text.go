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
	MaxLines int
	PaddingX int
	PaddingY int
}

func NewTruncatedText(content string, maxLines int) *TruncatedText {
	return &TruncatedText{Content: content, MaxLines: maxLines}
}

// NewPaddedTruncatedText creates a truncated text with padding.
func NewPaddedTruncatedText(content string, paddingX, paddingY int) *TruncatedText {
	return &TruncatedText{Content: content, MaxLines: 1, PaddingX: paddingX, PaddingY: paddingY}
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
