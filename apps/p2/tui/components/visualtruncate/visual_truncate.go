// pi: apps/p2/src/tui/components/visual-truncate.ts (copied verbatim from coding-agent interactive/components/visual-truncate.ts)
package visualtruncate

import (
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// VisualTruncateResult is VisualTruncateResult.
type VisualTruncateResult struct {
	// VisualLines are the visual lines to display.
	VisualLines []string
	// SkippedCount is the number of visual lines that were skipped (hidden).
	SkippedCount int
}

// TruncateToVisualLines truncates text to a maximum number of visual lines, accounting for line wrapping based on terminal width. paddingX is the
// horizontal padding for the Text component (0 when the result goes in a Box, which adds its own padding; 1 in a plain Container); keepStart keeps the
// first lines instead of the last ones.
func TruncateToVisualLines(text string, maxVisualLines, width, paddingX int, keepStart bool) VisualTruncateResult {
	if text == "" {
		return VisualTruncateResult{VisualLines: []string{}}
	}
	// A temporary Text component renders the visual lines.
	allVisualLines := tui.NewPaddedText(text, paddingX, 0, nil).Render(width)
	if len(allVisualLines) <= maxVisualLines {
		return VisualTruncateResult{VisualLines: allVisualLines}
	}
	var truncated []string
	if keepStart {
		truncated = allVisualLines[:maxVisualLines]
	} else {
		truncated = allVisualLines[len(allVisualLines)-maxVisualLines:]
	}
	return VisualTruncateResult{VisualLines: truncated, SkippedCount: len(allVisualLines) - maxVisualLines}
}

// VisualLinePreviewOptions are VisualLinePreviewOptions.
type VisualLinePreviewOptions struct {
	// Text is the styled text; it may contain newlines.
	Text           string
	MaxVisualLines int
	// KeepStart keeps the first visual lines (the hint goes after them); otherwise the last ones are kept, with the hint before.
	KeepStart bool
	// FormatHint is the styled hint line for the given number of hidden visual lines.
	FormatHint func(hidden int) string
}

// VisualLinePreview is collapsed tool output limited to a number of visual lines, like a shell command's output. Limiting logical lines instead lets a
// single long line (such as minified JSON) wrap across the whole screen. It caches its lines per width, since it renders on every frame for every
// result in the transcript.
type VisualLinePreview struct {
	options     VisualLinePreviewOptions
	cachedWidth int
	cachedLines []string
	cached      bool
}

// NewVisualLinePreview is the VisualLinePreview constructor.
func NewVisualLinePreview(options VisualLinePreviewOptions) *VisualLinePreview {
	return &VisualLinePreview{options: options}
}

// Render renders the preview at width.
func (p *VisualLinePreview) Render(width int) []string {
	if !p.cached || p.cachedWidth != width {
		preview := TruncateToVisualLines(p.options.Text, p.options.MaxVisualLines, width, 0, p.options.KeepStart)
		lines := preview.VisualLines
		if preview.SkippedCount > 0 {
			hint := widthx.TruncateToWidth(p.options.FormatHint(preview.SkippedCount), width, "...", false)
			if p.options.KeepStart {
				p.cachedLines = append(append([]string(nil), lines...), hint)
			} else {
				p.cachedLines = append([]string{hint}, lines...)
			}
		} else {
			p.cachedLines = lines
		}
		p.cachedWidth = width
		p.cached = true
	}
	return p.cachedLines
}

// Invalidate drops the cached lines.
func (p *VisualLinePreview) Invalidate() {
	p.cached = false
	p.cachedLines = nil
}
