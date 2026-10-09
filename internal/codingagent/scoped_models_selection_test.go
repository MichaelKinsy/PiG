package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
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
	m := NewInteractiveMode(nil, InteractiveModeOptions{ModelRegistry: registry, Settings: Settings{EnabledModels: []string{"unavailable-one", "unavailable-two"}}})
	m.initScopedModels()
	if len(m.scopedModelIDs) != 0 {
		t.Fatalf("unmatched settings leaked into the session scope: %v", m.scopedModelIDs)
	}
}

func TestScopedModelStartupUsesSessionScope(t *testing.T) {
	one := &ai.Model{ID: "one", ProviderMeta: ai.ProviderMetadata{ProviderID: "fixture"}}
	two := &ai.Model{ID: "two", ProviderMeta: ai.ProviderMetadata{ProviderID: "fixture"}}
	handle := &recordingCompactHandle{scopedModels: []extension.ScopedModel{{Model: two}, {Model: one}}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{
		SessionHandle: handle,
		Settings:      Settings{EnabledModels: []string{"fixture/one"}},
	})
	m.initScopedModels()
	if want := []string{"fixture/two", "fixture/one"}; !reflect.DeepEqual(m.scopedModelIDs, want) {
		t.Fatalf("interactive scope = %v, want Session scope %v", m.scopedModelIDs, want)
	}
}

// Pi keys scoped models by `${provider}/${id}` without collapsing an ID that already starts with the provider (interactive-mode.ts:5355,5363).
func TestScopedModelStartupKeepsProviderPrefixedIDs(t *testing.T) {
	auto := &ai.Model{ID: "fixture/auto", ProviderMeta: ai.ProviderMetadata{ProviderID: "fixture"}}
	handle := &recordingCompactHandle{scopedModels: []extension.ScopedModel{{Model: auto}}}
	m := NewInteractiveMode(nil, InteractiveModeOptions{SessionHandle: handle})
	m.initScopedModels()
	if want := []string{"fixture/fixture/auto"}; !reflect.DeepEqual(m.scopedModelIDs, want) {
		t.Fatalf("interactive scope = %v, want %v", m.scopedModelIDs, want)
	}
}

func TestScopedSelectionKeepsProviderPrefixedIDsInSessionScope(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"fixture":{"baseUrl":"http://127.0.0.1:9","api":"openai-completions","apiKey":"fake-key","models":[{"id":"fixture/auto"},{"id":"two"},{"id":"three"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	models := []tui.ModelItem{{FullID: "fixture/fixture/auto", Provider: "fixture"}, {FullID: "fixture/two", Provider: "fixture"}, {FullID: "fixture/three", Provider: "fixture"}}
	selection, _ := newScopedModelsSelection(models, nil, nil)
	handle := &recordingCompactHandle{}
	m := &InteractiveMode{opts: InteractiveModeOptions{ModelRegistry: NewModelRegistry(dir), SessionHandle: handle}}
	m.statusLine = NewFooterComponent(nil, "test", nil)
	selection.apply(m, []string{"fixture/fixture/auto", "fixture/two"})
	if got := len(handle.ScopedModels()); got != 2 {
		t.Fatalf("Session scope has %d models, want 2: the provider-prefixed model was dropped", got)
	}
	m.initScopedModels()
	if want := []string{"fixture/fixture/auto", "fixture/two"}; !reflect.DeepEqual(m.scopedModelIDs, want) {
		t.Fatalf("interactive scope = %v, want %v", m.scopedModelIDs, want)
	}
}

func TestScopedSelectionSynchronizesSessionScope(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"fixture":{"baseUrl":"http://127.0.0.1:9","api":"openai-completions","apiKey":"fake-key","models":[{"id":"one"},{"id":"two"},{"id":"three"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	models := []tui.ModelItem{{FullID: "fixture/one", Provider: "fixture"}, {FullID: "fixture/two", Provider: "fixture"}, {FullID: "fixture/three", Provider: "fixture"}}
	selection, _ := newScopedModelsSelection(models, nil, nil)
	handle := &recordingCompactHandle{}
	m := &InteractiveMode{opts: InteractiveModeOptions{ModelRegistry: registry, SessionHandle: handle}}
	m.statusLine = NewFooterComponent(nil, "test", nil)
	for _, tc := range []struct {
		name    string
		enabled []string
		want    []string
	}{
		{name: "ordered subset", enabled: []string{"fixture/two", "fixture/gone", "fixture/one"}, want: []string{"fixture/two", "fixture/one"}},
		{name: "replacement", enabled: []string{"fixture/three"}, want: []string{"fixture/three"}},
		{name: "all", enabled: []string{"fixture/three", "fixture/two", "fixture/one"}},
		{name: "unavailable only", enabled: []string{"fixture/gone"}},
		{name: "empty", enabled: []string{}},
		{name: "implicit all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection.apply(m, tc.enabled)
			var got []string
			for _, entry := range handle.ScopedModels() {
				got = append(got, modelSpec(entry.Model))
				if entry.ThinkingLevel != "" {
					t.Fatalf("selector retained a thinking override: %q", entry.ThinkingLevel)
				}
			}
			if !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(m.scopedModelIDs, tc.want) {
				t.Fatalf("Session scope = %v, interactive scope = %v, want %v", got, m.scopedModelIDs, tc.want)
			}
		})
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
