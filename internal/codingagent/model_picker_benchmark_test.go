package codingagent

import (
	"context"
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/benchalloc"
	"github.com/MichaelKinsy/PiG/tui"
)

// pickerScenario is a mode over a registry, the number of available models the picker lists in it and the number of models its scope holds.
type pickerScenario struct {
	m         *InteractiveMode
	available int
	scoped    int
}

// newPickerScenario builds the mode of a picker benchmark over registry. A scoped scenario scopes every available model, in reverse order, which exercises lookup without relying on catalog order. available is counted from the registry, not from the picker's items, so the benchmarks' divisor does not come from the code they measure.
func newPickerScenario(tb testing.TB, registry *ModelRegistry, scoped bool) pickerScenario {
	tb.Helper()
	m := &InteractiveMode{opts: InteractiveModeOptions{ModelRegistry: registry}}
	s := pickerScenario{m: m, available: len(registry.GetAvailableModelData())}
	if s.available == 0 {
		tb.Fatal("the picker lists no models")
	}
	if !scoped {
		return s
	}
	items := m.availableModelItems()
	if len(items) != s.available {
		tb.Fatalf("the picker lists %d models, the registry has %d available", len(items), s.available)
	}
	for _, item := range slices.Backward(items) {
		m.scopedModelIDs = append(m.scopedModelIDs, item.FQ())
	}
	s.scoped = len(m.scopedModelIDs)
	return s
}

// builtinPickerRegistry is a registry over the built-in catalog with Gemini and OpenAI configured: the path most users are on. The benchmarks run offline (PIG_OFFLINE): see scopedReopenSetup.
func builtinPickerRegistry(tb testing.TB) *ModelRegistry {
	tb.Helper()
	tb.Setenv("GEMINI_API_KEY", "benchmark-gemini")
	tb.Setenv("OPENAI_API_KEY", "benchmark-openai")
	tb.Setenv("PIG_OFFLINE", "1")
	return NewModelRegistry(tb.TempDir())
}

func snapshotAndRenderPass(b *testing.B, s pickerScenario) {
	items := s.m.availableModelItems()
	if len(items) != s.available {
		b.Fatalf("the picker listed %d models, want %d", len(items), s.available)
	}
	selector := tui.NewStaticModelSelectorComponent("Select model", nil, items, "")
	selector.Render(100)
}

// scopedReopenSetup returns the pass of a scoped reopen over s, closed by Escape; release must run when the benchmark ends.
//
// Every reopen starts the selector's own catalog refresh (StartModelSelectorTask), which the caller's Background context never cancels and which runs
// as a model task of the registry (RefreshModelCatalogs), so closing the selector ends the caller's wait but not the refresh. Whether that refresh
// ran, dialed the network, or was still unwinding when the next reopen began decided the count (5764 to 6538 allocs/op alone, 5613 or 5865 whole), so
// the benchmarks run offline and the pass joins both the mode's background tasks and the registry's model tasks: each reopen pays its own refresh,
// whole, every time. The snapshot-and-render benchmarks never open the selector and start no refresh, so they join nothing.
func scopedReopenSetup(s pickerScenario) (pass func(b *testing.B), release func()) {
	m := s.m
	registry := m.opts.ModelRegistry
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 35)
	m.installRenderDispatcher()
	m.setModalInputChannel(make(chan []byte, 1))
	input, release := m.acquireModalInputChannel()
	sc := m.buildSlashContext(context.Background())
	return func(b *testing.B) {
		input <- []byte("\x1b")
		if _, accepted := sc.PickModel(""); accepted {
			b.Fatal("Escape accepted a model")
		}
		m.backgroundTasks.Wait()
		registry.modelTasks.work.Wait()
	}, release
}

// BenchmarkModelPickerSnapshotAndRender is the picker's snapshot and render over the built-in catalog with two providers configured. The picker scans catalog state, never Session transcript history. Its allocs/op is allocations per 1,000 available models (internal/benchalloc), so a catalog refresh that adds models does not move the row while a model that costs more to list does.
func BenchmarkModelPickerSnapshotAndRender(b *testing.B) {
	s := newPickerScenario(b, builtinPickerRegistry(b), false)
	b.Run("per-1000-entries", func(b *testing.B) {
		benchalloc.Per1000Entries(b, s.available, func() { snapshotAndRenderPass(b, s) })
	})
}

// BenchmarkModelPickerScopedReopen reopens the picker over a scope of every available built-in model, over the same registry. Its allocs/op is allocations per 1,000 scoped models.
func BenchmarkModelPickerScopedReopen(b *testing.B) {
	s := newPickerScenario(b, builtinPickerRegistry(b), true)
	pass, release := scopedReopenSetup(s)
	defer release()
	b.Run("per-1000-entries", func(b *testing.B) {
		benchalloc.Per1000Entries(b, s.scoped, func() { pass(b) })
	})
}

// BenchmarkModelPickerSnapshotAndRenderCustomProvider is the same snapshot and render over models of a models.json provider: pickerBenchmarkModels entries of internal/catalogfixture, so the figure is a cost per operation that no catalog refresh moves. It exists because the custom-provider path is not the built-in one and nothing measured it (see BenchmarkModelPickerScopedReopenCustomProvider).
func BenchmarkModelPickerSnapshotAndRenderCustomProvider(b *testing.B) {
	s := newPickerScenario(b, fixtureModelRegistry(b), false)
	b.ReportAllocs()
	for b.Loop() {
		snapshotAndRenderPass(b, s)
	}
}

// BenchmarkModelPickerScopedReopenCustomProvider reopens the picker over a scope of pickerBenchmarkModels models of models.json providers. A resolved scope entry of such a provider recomposes the provider's model definitions, so a reopen costs about 12,000 allocations per scoped model (the built-in path about 80) and grows faster than linearly with the scope.
func BenchmarkModelPickerScopedReopenCustomProvider(b *testing.B) {
	s := newPickerScenario(b, fixtureModelRegistry(b), true)
	pass, release := scopedReopenSetup(s)
	defer release()
	b.ReportAllocs()
	for b.Loop() {
		pass(b)
	}
}
