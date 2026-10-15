package tui

import (
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

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
	padding := 0
	if len(paddingX) > 0 {
		padding = paddingX[0]
	}
	return TruncateToVisualLinesKeeping(text, maxVisualLines, width, padding, VisualKeepEnd)
}

// VisualKeep selects which visual lines a preview keeps.
type VisualKeep string

// The two ends of the text a preview can keep.
const (
	VisualKeepStart VisualKeep = "start"
	VisualKeepEnd   VisualKeep = "end"
)

// TruncateToVisualLinesKeeping is [TruncateToVisualLines] with the choice of the lines to keep: the last ones or the
// first ones.
func TruncateToVisualLinesKeeping(text string, maxVisualLines, width, paddingX int, keep VisualKeep) VisualTruncateResult {
	if text == "" {
		return VisualTruncateResult{}
	}
	all := NewPaddedText(text, paddingX, 0, nil).Render(width)
	if len(all) <= maxVisualLines {
		return VisualTruncateResult{VisualLines: all}
	}
	// JavaScript's slice(-0) is slice(0), so keeping zero end lines returns every line while still counting them as skipped.
	truncated := all
	if maxVisualLines > 0 {
		truncated = all[len(all)-maxVisualLines:]
	}
	if keep == VisualKeepStart {
		truncated = all[:maxVisualLines]
	}
	return VisualTruncateResult{VisualLines: truncated, SkippedCount: len(all) - maxVisualLines}
}

// VisualLinePreviewOptions configure a [VisualLinePreview].
type VisualLinePreviewOptions struct {
	// Text is styled text; it may contain newlines.
	Text           string
	MaxVisualLines int
	// Keep selects the visual lines to keep. The hint goes before kept end lines and after kept start lines.
	Keep VisualKeep
	// FormatHint returns the styled hint line for the number of hidden visual lines.
	FormatHint func(hidden int) string
}

// VisualLinePreview is collapsed tool output limited to a number of visual lines, like bash output. Limiting logical
// lines instead lets a single long line (such as minified JSON) wrap across the whole screen. It caches its lines per
// width, since it renders on every frame for every result in the transcript.
//
// Ports packages/coding-agent/src/modes/interactive/components/visual-truncate.ts (VisualLinePreview).
type VisualLinePreview struct {
	options VisualLinePreviewOptions

	mu          sync.Mutex
	cachedWidth int
	cachedLines []string
	cached      bool
}

// NewVisualLinePreview returns a preview of options.Text.
func NewVisualLinePreview(options VisualLinePreviewOptions) *VisualLinePreview {
	return &VisualLinePreview{options: options}
}

// Render returns the preview's lines for width.
func (p *VisualLinePreview) Render(width int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.cached || p.cachedWidth != width {
		o := p.options
		preview := TruncateToVisualLinesKeeping(o.Text, o.MaxVisualLines, width, 0, o.Keep)
		lines := preview.VisualLines
		if preview.SkippedCount > 0 {
			hint := widthx.TruncateToWidth(o.FormatHint(preview.SkippedCount), width, "...", false)
			if o.Keep == VisualKeepStart {
				lines = append(slices.Clone(lines), hint)
			} else {
				lines = append([]string{hint}, lines...)
			}
		}
		p.cachedLines, p.cachedWidth, p.cached = lines, width, true
	}
	return p.cachedLines
}

// Invalidate drops the cached lines.
func (p *VisualLinePreview) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cached, p.cachedLines = false, nil
}
