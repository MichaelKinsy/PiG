package sdk

import (
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// VisualTruncateResult is pi-coding-agent's VisualTruncateResult: the visual lines kept and how many were dropped.
type VisualTruncateResult struct {
	VisualLines  []string
	SkippedCount int
}

// VisualTruncateOption is one of truncateToVisualLines' optional arguments.
type VisualTruncateOption func(*visualTruncateOptions)

type visualTruncateOptions struct {
	paddingX int
	keep     string
}

// WithVisualPaddingX is truncateToVisualLines' paddingX: the horizontal padding of the Text the lines are laid out with. Pi's default is 0, right for a result that
// is placed in a Box (the Box adds its own padding); use 1 for a plain Container.
func WithVisualPaddingX(paddingX int) VisualTruncateOption {
	return func(o *visualTruncateOptions) { o.paddingX = paddingX }
}

// WithVisualKeep is truncateToVisualLines' keep: "end" (Pi's default) keeps the last visual lines and "start" the first ones. Any other value keeps the last, as Pi's
// `keep === "start" ? ... : ...` does.
func WithVisualKeep(keep string) VisualTruncateOption {
	return func(o *visualTruncateOptions) { o.keep = keep }
}

// TruncateToVisualLines is pi-coding-agent's truncateToVisualLines: text wrapped at width as a Text with horizontal padding paddingX renders it, cut to at most
// maxVisualLines visual lines, from the end unless WithVisualKeep("start") is given.
func TruncateToVisualLines(text string, maxVisualLines, width int, options ...VisualTruncateOption) VisualTruncateResult {
	o := visualTruncateOptions{keep: "end"}
	for _, option := range options {
		option(&o)
	}
	if text == "" {
		return VisualTruncateResult{VisualLines: []string{}}
	}
	all := renderPlainText(text, o.paddingX, width)
	if len(all) <= maxVisualLines {
		return VisualTruncateResult{VisualLines: all}
	}
	var kept []string
	if o.keep == "start" {
		kept = jsSlice(all, 0, maxVisualLines)
	} else {
		kept = jsSliceFrom(all, -maxVisualLines)
	}
	return VisualTruncateResult{VisualLines: kept, SkippedCount: len(all) - maxVisualLines}
}

// renderPlainText is pi-tui's Text(text, paddingX, 0).render(width): tabs as three spaces, the text wrapped in the width less the padding on both sides (the padding
// shrinks to fit a narrow width), every line padded to the width.
func renderPlainText(text string, paddingX, width int) []string {
	if jsTrimmedEmpty(text) {
		return []string{}
	}
	normalized := strings.ReplaceAll(text, "\t", "   ")
	paddingX = min(paddingX, max(0, floorDiv(width-1, 2)))
	contentWidth := max(1, width-paddingX*2)
	margin := strings.Repeat(" ", max(0, paddingX))
	wrapped := tui.WrapTextWithAnsi(normalized, contentWidth)
	lines := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		withMargins := margin + line + margin
		lines = append(lines, withMargins+strings.Repeat(" ", max(0, width-tui.VisibleWidth(withMargins))))
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// floorDiv is Math.floor(a / b) for b > 0.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// jsTrimmedEmpty is `text.trim() === ""`: only ECMAScript white space and line terminators.
func jsTrimmedEmpty(text string) bool {
	for _, r := range text {
		switch {
		case r == '\t', r == '\n', r == '\v', r == '\f', r == '\r', r == ' ', r == 0xA0, r == 0x1680, r >= 0x2000 && r <= 0x200A,
			r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000, r == 0xFEFF:
		default:
			return false
		}
	}
	return true
}

// jsSlice is Array.prototype.slice(start, end) for 0 <= start.
func jsSlice(lines []string, start, end int) []string {
	n := len(lines)
	if end < 0 {
		end = max(0, n+end)
	}
	end = min(end, n)
	if start >= end {
		return []string{}
	}
	return lines[start:end]
}

// jsSliceFrom is Array.prototype.slice(start) with a negative start counting from the end; slice(-0) is slice(0).
func jsSliceFrom(lines []string, start int) []string {
	n := len(lines)
	if start < 0 {
		start = max(0, n+start)
	}
	if start >= n {
		return []string{}
	}
	return lines[start:]
}
