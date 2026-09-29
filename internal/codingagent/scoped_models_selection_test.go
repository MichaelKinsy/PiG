package codingagent

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestScopedSelectionKeepsUnavailableIDsOutOfCycling(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/modes/interactive/interactive-mode.ts:5242-5279
	models := []tui.ModelItem{{FullID: "fixture/one", Provider: "fixture", Name: "One"}, {FullID: "fixture/two", Provider: "fixture", Name: "Two"}, {FullID: "fixture/three", Provider: "fixture", Name: "Three"}}
	for _, tc := range []struct {
		name                      string
		enabled, scope, persisted []string
	}{
		{name: "all implicit"},
		{name: "none", enabled: []string{}, persisted: []string{}},
		{name: "unavailable only", enabled: []string{"fixture/gone"}, persisted: []string{"fixture/gone"}},
		{name: "partial with unavailable", enabled: []string{"fixture/two", "fixture/gone", "fixture/one"}, scope: []string{"fixture/two", "fixture/one"}, persisted: []string{"fixture/two", "fixture/gone", "fixture/one"}},
		{name: "all explicit", enabled: []string{"fixture/three", "fixture/two", "fixture/one"}},
		{name: "all plus unavailable", enabled: []string{"fixture/one", "fixture/two", "fixture/three", "fixture/gone"}, persisted: []string{"fixture/one", "fixture/two", "fixture/three", "fixture/gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection, _ := newScopedModelsSelection(models, nil, nil)
			if got := selection.scopeIDs(tc.enabled); !reflect.DeepEqual(got, tc.scope) {
				t.Errorf("scope=%v, want %v", got, tc.scope)
			}
			if got := selection.persistedIDs(tc.enabled); !reflect.DeepEqual(got, tc.persisted) {
				t.Errorf("persisted=%#v, want %#v", got, tc.persisted)
			}
		})
	}
	selection, initial := newScopedModelsSelection(models, []string{"fixture/t*:high", "fixture/gone", "fixture/gone", "one:invalid"}, nil)
	if want := []string{"fixture/two", "fixture/three", "fixture/one", "fixture/gone"}; !reflect.DeepEqual(initial, want) {
		t.Fatalf("initial=%v, want %v", initial, want)
	}
	selection.updateAvailable(append(models, tui.ModelItem{FullID: "fixture/gone", Provider: "fixture", Name: "Gone"}))
	if got, want := selection.configuredIDs(), []string{"fixture/two", "fixture/three", "fixture/gone", "fixture/one"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("refreshed=%v, want %v", got, want)
	}
}

func TestScopedModelStartupResolvesConfiguredPatterns(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	registry := NewModelRegistry(t.TempDir())
	m := NewInteractiveMode(InteractiveOptions{ModelRegistry: registry, Settings: Settings{EnabledModels: []string{"unavailable-one", "unavailable-two"}}})
	m.initScopedModels()
	if len(m.scopedModelIDs) != 0 {
		t.Fatalf("unmatched settings leaked into the session scope: %v", m.scopedModelIDs)
	}
}

func BenchmarkScopedModelsSelection(b *testing.B) {
	models := make([]tui.ModelItem, 1000)
	for i := range models {
		models[i] = tui.ModelItem{Provider: "fixture", FullID: fmt.Sprintf("fixture/model-%04d", i), Name: "Model"}
	}
	b.ReportAllocs()
	for b.Loop() {
		s, enabled := newScopedModelsSelection(models, []string{"fixture/model-0001", "fixture/missing"}, nil)
		_ = s.scopeIDs(enabled)
	}
}
