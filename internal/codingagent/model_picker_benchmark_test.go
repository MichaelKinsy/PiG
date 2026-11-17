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
	m := &InteractiveMode{opts: InteractiveModeOptions{ModelRegistry: NewModelRegistry(b.TempDir())}}
	b.ReportAllocs()
	for b.Loop() {
		items := m.availableModelItems()
		selector := tui.NewStaticModelSelectorComponent("Select model", nil, items, "")
		selector.Render(100)
	}
}

// Every reopen starts the selector's own catalog refresh (StartModelSelectorTask), which the caller's Background context never cancels and which runs
// as a model task of the registry (RefreshModelCatalogs), so closing the selector ends the caller's wait but not the refresh. Whether that refresh
// ran, dialed the network, or was still unwinding when the next reopen began decided the count (5764 to 6538 allocs/op alone, 5613 or 5865 whole), so
// the benchmark runs offline and joins both the mode's background tasks and the registry's model tasks inside each operation: each reopen pays its own
// refresh, whole, every time. One unmeasured reopen first pays the one-time package state.
func BenchmarkModelPickerScopedReopen(b *testing.B) {
	b.Setenv("GEMINI_API_KEY", "benchmark-gemini")
	b.Setenv("OPENAI_API_KEY", "benchmark-openai")
	b.Setenv("PIG_OFFLINE", "1")
	registry := NewModelRegistry(b.TempDir())
	m := &InteractiveMode{opts: InteractiveModeOptions{ModelRegistry: registry}}
	items := m.availableModelItems()
	// A full catalog in reverse scope order exercises lookup without relying on catalog order.
	for _, item := range slices.Backward(items) {
		m.scopedModelIDs = append(m.scopedModelIDs, item.FQ())
	}
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 35)
	m.installRenderDispatcher()
	m.setModalInputChannel(make(chan []byte, 1))
	input, release := m.acquireModalInputChannel()
	defer release()
	sc := m.buildSlashContext(context.Background())
	reopen := func() {
		input <- []byte("\x1b")
		if _, accepted := sc.PickModel(""); accepted {
			b.Fatal("Escape accepted a model")
		}
		m.backgroundTasks.Wait()
		registry.modelTasks.work.Wait()
	}
	reopen()
	b.ReportAllocs()
	for b.Loop() {
		reopen()
	}
}
