package tui

import (
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// StatusIndicator renders a loader either standalone or inside an editor border.
// Its owner advances animation and disposes operation timers on replacement.
type StatusIndicator struct {
	*Loader
	Kind string
}

func (s *StatusIndicator) RenderInBorder(width int) string {
	lines := s.Render(width + 2)
	if len(lines) < 2 {
		return ""
	}
	line := strings.TrimPrefix(lines[1], " ")
	return widthx.TruncateToWidth(strings.TrimRightFunc(line, func(r rune) bool { return r == '\ufeff' || (r != '\u0085' && unicode.IsSpace(r)) }), width, "", false)
}

func (s *StatusIndicator) RenderSpinnerInBorder(width int) string {
	return widthx.TruncateToWidth(s.renderedIndicator(), width, "", false)
}

// IdleStatus reserves the standalone loader's two rows after clearing it.
type IdleStatus struct{}

func (*IdleStatus) Invalidate() {}
func (*IdleStatus) Render(width int) []string {
	line := strings.Repeat(" ", max(0, width))
	return []string{line, line}
}
