package codingagent

import (
	"context"
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// The picker scans catalog state, never Session transcript history.
func BenchmarkModelPickerSnapshotAndRender(b *testing.B) {
	b.Setenv("GEMINI_API_KEY", "benchmark-gemini")
	b.Setenv("OPENAI_API_KEY", "benchmark-openai")
	m := &InteractiveMode{opts: InteractiveOptions{ModelRegistry: NewModelRegistry(b.TempDir())}}
	b.ReportAllocs()
	for b.Loop() {
		items := m.availableModelItems()
		selector := tui.NewModelSelector("Select model", nil, items, "")
		selector.Render(100)
	}
}

func BenchmarkModelPickerScopedReopen(b *testing.B) {
	b.Setenv("GEMINI_API_KEY", "benchmark-gemini")
	b.Setenv("OPENAI_API_KEY", "benchmark-openai")
	m := &InteractiveMode{opts: InteractiveOptions{ModelRegistry: NewModelRegistry(b.TempDir())}}
	items := m.availableModelItems()
	// A full catalog in reverse scope order exercises lookup without relying on catalog order.
	for _, item := range slices.Backward(items) {
		m.scopedModelIDs = append(m.scopedModelIDs, item.FQ())
	}
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 35)
	m.setModalInputChannel(make(chan []byte, 1))
	input, release := m.acquireModalInputChannel()
	defer release()
	sc := m.buildSlashContext(context.Background())
	b.ReportAllocs()
	for b.Loop() {
		input <- []byte("\x1b")
		if _, accepted := sc.PickModel(""); accepted {
			b.Fatal("Escape accepted a model")
		}
	}
}
