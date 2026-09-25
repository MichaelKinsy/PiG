package tui

// StaticText is a Component that renders fixed pre-rendered lines.
// Used for content that never changes (e.g. login art/banner) but must
// survive full redraws because it's part of the TUI render buffer.
type StaticText struct {
	lines []string
}

// NewStaticText creates a StaticText component from pre-rendered lines.
func NewStaticText(lines []string) *StaticText {
	return &StaticText{lines: lines}
}

func (s *StaticText) Render(_ int) []string { return s.lines }
func (s *StaticText) Invalidate()           {}
func (s *StaticText) IsDirty() bool         { return false }
func (s *StaticText) NeedsRedraw() bool     { return false }
