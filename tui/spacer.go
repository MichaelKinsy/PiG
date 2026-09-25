package tui

// spacer.go: blank-line spacer component.
//
// Ports upstream packages/tui/src/components/spacer.ts.

// Spacer component that renders empty lines.
type Spacer struct {
	invalidatable
	Lines int
}

func NewSpacer(n int) *Spacer { return &Spacer{Lines: n} }

// SetLines updates the number of blank lines.
func (s *Spacer) SetLines(n int) {
	s.Lines = n
	s.Invalidate()
}

func (s *Spacer) Render(_ int) []string {
	result := make([]string, s.Lines)
	for i := range result {
		result[i] = ""
	}
	return result
}
