package tui

// text.go: wrapped text component.
//
// Ports upstream packages/tui/src/components/text.ts.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Text component - displays multi-line text with word wrapping.
type Text struct {
	invalidatable
	Content    string
	PaddingX   int
	PaddingY   int
	CustomBgFn func(string) string

	cachedText  string
	cachedWidth int
	cachedLines []string
	cacheValid  bool
	// rendered reports that Render drew the text. wrapCache keeps the padded rows of each line of the text drawn last, for
	// the width and padding in wrapWidth and wrapPaddingX; it is made when a drawn text changes and dropped by Invalidate.
	rendered     bool
	wrapCache    *widthx.LineWrapCache
	wrapWidth    int
	wrapPaddingX int
}

// NewText creates a text component. pig keeps zero padding as the default for
// existing call sites; upstream-style padding is available via fields.
func NewText(content string) *Text { return &Text{Content: content} }

// NewPaddedText creates a text component with explicit padding.
func NewPaddedText(content string, paddingX, paddingY int, bgFn func(string) string) *Text {
	return &Text{Content: content, PaddingX: paddingX, PaddingY: paddingY, CustomBgFn: bgFn}
}

// SetText replaces the text. The same text keeps the wrapped lines, since renderers often set the text they showed
// before on every update (text.ts setText).
func (t *Text) SetText(content string) {
	if content == t.Content {
		return
	}
	t.Content = content
	// The text changed after it was drawn: the lines it keeps are drawn again from the rows of the last text, not wrapped again. A text
	// that is never drawn twice with different content holds no such rows.
	t.invalidatable.Invalidate()
	t.cacheValid = false
	t.cachedLines = nil
	if t.rendered && t.wrapCache == nil {
		t.wrapCache = &widthx.LineWrapCache{}
	}
}

// SetPaddingX sets the horizontal padding and invalidates the cached lines (text.ts setPaddingX).
func (t *Text) SetPaddingX(paddingX int) {
	t.PaddingX = paddingX
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
	t.wrapCache = nil
}

func (t *Text) Render(width int) []string {
	if t.cacheValid && t.cachedText == t.Content && t.cachedWidth == width {
		return t.cachedLines
	}
	if t.Content == "" || jsstring.Trim(t.Content) == "" {
		t.cachedText = t.Content
		t.cachedWidth = width
		t.cachedLines = []string{}
		t.cacheValid = true
		t.rendered = true
		return t.cachedLines
	}

	normalized := strings.ReplaceAll(t.Content, "\t", "   ")
	// Reduce margins when necessary so content and padding fit within the
	// available width (upstream text.ts clamps paddingX the same way).
	paddingX := min(t.PaddingX, max(0, (width-1)/2))
	contentWidth := max(1, width-paddingX*2)
	margin := strings.Repeat(" ", paddingX)
	var contentLines []string
	if t.wrapCache != nil && t.CustomBgFn == nil {
		// The lines the last text left unchanged keep their padded rows: a streamed tool call changes the end of its text.
		if t.wrapPaddingX != paddingX || t.wrapWidth != width {
			t.wrapCache.Reset()
		}
		t.wrapPaddingX, t.wrapWidth = paddingX, width
		contentLines = t.wrapCache.Wrap(normalized, contentWidth, func(row string) string { return padToWidth(margin+row+margin, width) })
	} else {
		wrapped := widthx.WrapTextWithAnsi(normalized, contentWidth)
		contentLines = make([]string, 0, len(wrapped))
		for _, line := range wrapped {
			lineWithMargins := margin + line + margin
			if t.CustomBgFn != nil {
				contentLines = append(contentLines, t.CustomBgFn(padToWidth(lineWithMargins, width)))
			} else {
				contentLines = append(contentLines, padToWidth(lineWithMargins, width))
			}
		}
	}

	// The background function runs once per padding row, after the content rows, as text.ts calls it.
	emptyLine := strings.Repeat(" ", max(width, 0))
	emptyLines := make([]string, max(t.PaddingY, 0))
	for i := range emptyLines {
		emptyLines[i] = emptyLine
		if t.CustomBgFn != nil {
			emptyLines[i] = t.CustomBgFn(emptyLine)
		}
	}
	result := make([]string, 0, capHint(len(contentLines), 2*t.PaddingY))
	result = append(result, emptyLines...)
	result = append(result, contentLines...)
	result = append(result, emptyLines...)

	t.cachedText = t.Content
	t.cachedWidth = width
	t.cachedLines = result
	t.cacheValid = true
	t.rendered = true
	return result
}

func padToWidth(line string, width int) string {
	pad := max(0, width-widthx.VisibleWidth(line))
	return line + strings.Repeat(" ", pad)
}
