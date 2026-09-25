package tui

// diff_component.go: diff display component.
//
// Ports upstream components/diff.ts (147 LOC) as a Component wrapper
// around the existing RenderDiff function in diff_render.go.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// DiffComponent renders a diff string as colored output.
// It wraps RenderDiff from diff_render.go into the Component interface.
type DiffComponent struct {
	invalidatable
	diffText string
	filePath string
}

// NewDiffComponent creates a diff display component.
func NewDiffComponent(diffText, filePath string) *DiffComponent {
	return &DiffComponent{diffText: diffText, filePath: filePath}
}

// SetDiff updates the diff text.
func (d *DiffComponent) SetDiff(text string) {
	d.diffText = text
	d.Invalidate()
}

// Render produces colored diff lines.
func (d *DiffComponent) Render(width int) []string {
	if d.diffText == "" {
		return nil
	}
	rendered := RenderDiff(d.diffText)
	// Upstream feeds renderDiff output through Text, which wraps to width.
	var out []string
	for line := range strings.SplitSeq(rendered, "\n") {
		if width <= 0 || widthx.VisibleWidth(line) <= width {
			out = append(out, line)
			continue
		}
		out = append(out, widthx.WrapTextWithAnsi(line, width)...)
	}
	return out
}
