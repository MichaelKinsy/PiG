package tui

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkRenderDiff is one render of a 400-row diff of changed, added and removed lines, as the edit tool's result shows it.
func BenchmarkRenderDiff(b *testing.B) {
	var rows []string
	for line := 1; line <= 100; line++ {
		rows = append(rows,
			fmt.Sprintf(" %3d     unchanged context line %d", line, line),
			fmt.Sprintf("-%3d     \told value %d in the line", line+1, line),
			fmt.Sprintf("+%3d     \tnew value %d in the line", line+1, line),
			fmt.Sprintf(" %3d     unchanged trailing line %d", line+2, line))
	}
	diff := strings.Join(rows, "\n")
	b.ReportAllocs()
	for b.Loop() {
		_ = RenderDiff(diff)
	}
}
