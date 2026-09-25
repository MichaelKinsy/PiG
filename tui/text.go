package tui

// text.go: wrapped text component.
//
// Ports upstream packages/tui/src/components/text.ts.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Text component - displays multi-line text with word wrapping.
type Text struct {
	invalidatable
	Content    string
	PaddingX   int
	PaddingY   int
	CustomBgFn func(string) string
	Style      string // pig compatibility: extra ANSI prefix applied per line

	cachedText  string
	cachedWidth int
	cachedLines []string
	cacheValid  bool
}

// NewText creates a text component. pig keeps zero padding as the default for
// existing call sites; upstream-style padding is available via fields.
func NewText(content string) *Text { return &Text{Content: content} }

// NewPaddedText creates a text component with explicit padding.
func NewPaddedText(content string, paddingX, paddingY int, bgFn func(string) string) *Text {
	return &Text{Content: content, PaddingX: paddingX, PaddingY: paddingY, CustomBgFn: bgFn}
}

func (t *Text) SetText(content string) {
	t.Content = content
	t.Invalidate()
}

func (t *Text) SetCustomBgFn(bgFn func(string) string) {
	t.CustomBgFn = bgFn
	t.Invalidate()
}

func (t *Text) Invalidate() {
	t.invalidatable.Invalidate()
	t.cacheValid = false
	t.cachedLines = nil
}

func (t *Text) Render(width int) []string {
	if t.cacheValid && t.cachedText == t.Content && t.cachedWidth == width {
		return t.cachedLines
	}
	if t.Content == "" || strings.TrimSpace(t.Content) == "" {
		t.cachedText = t.Content
		t.cachedWidth = width
		t.cachedLines = []string{}
		t.cacheValid = true
		return t.cachedLines
	}

	normalized := strings.ReplaceAll(t.Content, "\t", "   ")
	// Reduce margins when necessary so content and padding fit within the
	// available width (upstream text.ts clamps paddingX the same way).
	paddingX := min(t.PaddingX, max(0, (width-1)/2))
	contentWidth := max(1, width-paddingX*2)
	wrapped := widthx.WrapTextWithAnsi(normalized, contentWidth)
	left := strings.Repeat(" ", paddingX)
	right := strings.Repeat(" ", paddingX)
	contentLines := make([]string, 0, len(wrapped))

	for _, line := range wrapped {
		lineWithMargins := left + line + right
		if t.Style != "" {
			// Close common text styles without resetting background color.
			lineWithMargins = t.Style + lineWithMargins + SGRFgReset + SGRBoldDimReset + SGRItalicReset + SGRUnderlineReset + SGRStrikeReset + SGRInverseReset
		}
		if t.CustomBgFn != nil {
			contentLines = append(contentLines, t.CustomBgFn(padToWidth(lineWithMargins, width)))
		} else {
			contentLines = append(contentLines, padToWidth(lineWithMargins, width))
		}
	}

	emptyLine := strings.Repeat(" ", max(width, 0))
	if t.CustomBgFn != nil {
		emptyLine = t.CustomBgFn(emptyLine)
	}
	result := make([]string, 0, capHint(len(contentLines), 2*t.PaddingY))
	for range t.PaddingY {
		result = append(result, emptyLine)
	}
	result = append(result, contentLines...)
	for range t.PaddingY {
		result = append(result, emptyLine)
	}

	t.cachedText = t.Content
	t.cachedWidth = width
	t.cachedLines = result
	t.cacheValid = true
	return result
}

func padToWidth(line string, width int) string {
	pad := max(0, width-widthx.VisibleWidth(line))
	return line + strings.Repeat(" ", pad)
}
