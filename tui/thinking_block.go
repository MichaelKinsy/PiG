package tui

import (
	"github.com/rivo/uniseg"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ThinkingBlock renders one reasoning trace.
// Hidden blocks show `Thinking...`. Visible blocks use dim italic text.

// thinkingHiddenSGR is dim + italic: matches upstream's italic(fg("thinkingText"))
// on a line renderer. "thinkingText" = "gray" in dark.json.
const thinkingHiddenSGR = "\033[2m\033[3m"

// thinkingVisibleSGR is gray fg + italic. Color #909090 is approximately
// upstream's "gray" thinkingText (#808080-ish). We use a direct ANSI gray
// (\033[90m = bright-black / dark-gray) + italic so it works on any terminal.
const thinkingVisibleSGR = "\033[90m\033[3m"

const thinkingHiddenResetSGR = SGRBoldDimReset + SGRItalicReset
const thinkingVisibleResetSGR = SGRFgReset + SGRItalicReset
const thinkingHiddenLabel = "Thinking..."

// ThinkingBlock is a TUI component that renders a thinking/reasoning block.
type ThinkingBlock struct {
	invalidatable
	content string
	hidden  bool
}

// NewThinkingBlock creates a thinking block. Pass hidden=true when the user
// has toggled visibility off (Ctrl+T). Content starts empty (invisible).
func NewThinkingBlock(hidden bool) *ThinkingBlock {
	return &ThinkingBlock{hidden: hidden}
}

// SetContent updates the thinking text (streaming or final).
func (b *ThinkingBlock) SetContent(content string) {
	b.content = content
	b.Invalidate()
}

// SetHidden controls whether the full text or a "Thinking..." indicator is shown.
func (b *ThinkingBlock) SetHidden(hidden bool) {
	b.hidden = hidden
	b.Invalidate()
}

// Content returns the current thinking text.
func (b *ThinkingBlock) Content() string { return b.content }

// Render implements Component.
// Returns nil/empty when content is empty (invisible, takes zero height).
func (b *ThinkingBlock) Render(width int) []string {
	if b.content == "" {
		return nil
	}
	if width < 1 {
		width = 1
	}
	if b.hidden {
		var hidden []string
		for _, chunk := range wrapLineByWidth(thinkingHiddenLabel, width) {
			hidden = append(hidden, thinkingHiddenSGR+chunk+thinkingHiddenResetSGR)
		}
		return hidden
	}

	// Wrap content lines column-aware (widthx), apply gray+italic SGR per line.
	var out []string
	for _, rawLine := range splitLines(b.content) {
		wrapped := wrapLineByWidth(rawLine, width)
		for _, chunk := range wrapped {
			out = append(out, thinkingVisibleSGR+chunk+thinkingVisibleResetSGR)
		}
	}
	return out
}

// splitLines splits s on newlines, preserving empty lines.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

// wrapLineByWidth breaks a single no-newline string into chunks of at most
// width terminal columns. Segments by grapheme cluster and measures each
// cluster with widthx.VisibleWidth, so width comes from the one canonical
// width function (column-aware, never rune/byte counts).
func wrapLineByWidth(line string, width int) []string {
	if widthx.VisibleWidth(line) <= width {
		return []string{line}
	}
	var out []string
	chunkStart := 0
	colCount := 0
	g := uniseg.NewGraphemes(line)
	for g.Next() {
		from, _ := g.Positions()
		gw := widthx.VisibleWidth(g.Str())
		if colCount+gw > width && colCount > 0 {
			out = append(out, line[chunkStart:from])
			chunkStart = from
			colCount = 0
		}
		colCount += gw
	}
	if chunkStart < len(line) {
		out = append(out, line[chunkStart:])
	}
	return out
}
