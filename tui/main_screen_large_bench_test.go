package tui

import (
	"io"
	"strings"
	"testing"
)

func BenchmarkMainScreenLargeFrames(b *testing.B) {
	a := "\x1b_Ga=T,f=100;" + strings.Repeat("A", 1200000) + "\x1b\\"
	z := "\x1b_Ga=T,f=100;" + strings.Repeat("B", 1200000) + "\x1b\\"
	for _, mode := range []string{"full", "differential"} {
		b.Run(mode, func(b *testing.B) {
			ui := NewWithOutput(io.Discard, 80, 24)
			component := &recordingComponent{lines: []string{a, a}}
			ui.Add(component)
			ui.Render()
			toggle := false
			b.ReportAllocs()
			for b.Loop() {
				toggle = !toggle
				if toggle {
					component.lines[0], component.lines[1] = z, z
				} else {
					component.lines[0], component.lines[1] = a, a
				}
				if mode == "full" {
					ui.ForceFullRender()
				}
				ui.Render()
			}
		})
	}
}
