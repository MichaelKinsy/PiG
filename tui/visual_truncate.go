package tui

// visual_truncate.go: visual line truncation utility.
//
// Ports upstream visual-truncate.ts (50 LOC).
// Truncates text to a maximum number of visual lines (from the end),
// accounting for line wrapping at terminal width.

// VisualTruncateResult holds truncated lines and a skip count.
type VisualTruncateResult struct {
	VisualLines  []string
	SkippedCount int
}

// TruncateToVisualLines truncates text to maxVisualLines from the end, wrapping lines at the given width. The optional padding matches Text's horizontal padding.
func TruncateToVisualLines(text string, maxVisualLines, width int, paddingX ...int) VisualTruncateResult {
	if text == "" {
		return VisualTruncateResult{}
	}

	padding := 0
	if len(paddingX) > 0 {
		padding = paddingX[0]
	}
	t := NewPaddedText(text, padding, 0, nil)
	all := t.Render(width)

	if len(all) <= maxVisualLines {
		return VisualTruncateResult{VisualLines: all}
	}

	truncated := all[len(all)-maxVisualLines:]
	return VisualTruncateResult{
		VisualLines:  truncated,
		SkippedCount: len(all) - maxVisualLines,
	}
}
