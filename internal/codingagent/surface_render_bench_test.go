package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// BenchmarkLongTranscriptSurfaceRender renders an unchanged long transcript of 500 custom messages and 500 expanded summaries, as every frame of a
// long session does; frame-bytes is the size of the rendered frame.
func BenchmarkLongTranscriptSurfaceRender(b *testing.B) {
	m := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 100, 30), outputPad: 1, toolsExpanded: true}
	for i := 0; i < 500; i++ {
		m.appendCustomMessage(CustomMessageEntry{CustomType: "note", Content: "custom message **body** with `code` and some words to wrap across the row", Display: true}, messageMeta{})
		m.addCompactionSummary("summary of earlier work: "+strings.Repeat("kept the plan, ", 6), 1000+i, nil, messageMeta{})
	}
	frame := m.chatContainer.Render(100)
	bytes := 0
	for _, line := range frame {
		bytes += len(line) + 1
	}
	b.ReportAllocs()
	b.ReportMetric(float64(bytes), "frame-bytes")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.chatContainer.Render(100)
	}
}
