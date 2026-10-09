package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestUnavailableScopedModelsUpstream6949(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	for _, tc := range []struct {
		name                                 string
		models                               string
		settings, scope, wantRows, wantScope []string
		key                                  string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6949-unavailable-scoped-model.test.ts:94
		{name: "passes unmatched settings patterns to the selector with one combined resolution", settings: []string{"fixture/unavailable-one", "fixture/unavailable-two"}, wantRows: []string{"fixture/unavailable-one [unavailable]", "fixture/unavailable-two [unavailable]"}},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6949-unavailable-scoped-model.test.ts:114
		{name: "opens when only a session-scoped model is unavailable", scope: []string{"fixture/unavailable"}, wantRows: []string{"fixture/unavailable [unavailable]"}},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6949-unavailable-scoped-model.test.ts:132
		{name: "does not clear a partial scope when an enabled model is unavailable", models: `[{"id":"one","name":"One"},{"id":"two","name":"Two"},{"id":"three","name":"Three"}]`, settings: []string{"fixture/one", "fixture/two", "fixture/unavailable"}, scope: []string{"fixture/one", "fixture/two"}, key: "\x1b[1;3B", wantRows: []string{"one [fixture]", "two [fixture]", "three [fixture]"}, wantScope: []string{"fixture/two", "fixture/one"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.models != "" {
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"fixture":{"baseUrl":"https://fixture.test","api":"openai-completions","apiKey":"fixture-key","models":`+tc.models+`}}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			registry := NewModelRegistry(dir)
			registry.SetModelsStore(ai.NewInMemoryModelsStore())
			synctest.Test(t, func(t *testing.T) {
				m, _ := newExtensionDialogProbeSized(t, 100, 40)
				m.opts.ModelRegistry, m.opts.AgentDir = registry, dir
				m.opts.Settings.EnabledModels = tc.settings
				m.scopedModelIDs = tc.scope
				m.runCtx, m.statusLine = t.Context(), NewFooterComponent(nil, "", nil)
				done := make(chan struct{})
				go func() { m.showScopedModels(); close(done) }()
				synctest.Wait()
				input, _ := m.modalRoute()
				if input == nil {
					<-done
					t.Fatal("scoped-model selector did not open")
				}
				defer func() { input <- []byte("\x1b"); <-done }()
				rendered := stripANSI(strings.Join(m.editorContainer.Render(100), "\n"))
				for _, row := range tc.wantRows {
					if !strings.Contains(rendered, row) {
						t.Errorf("missing %q in %q", row, rendered)
					}
				}
				if tc.key != "" {
					for _, id := range tc.scope {
						if strings.Contains(rendered, id+" [unavailable]") {
							t.Errorf("available scoped model rendered unavailable: %q", rendered)
						}
					}
					input <- []byte(tc.key)
					synctest.Wait()
					if !reflect.DeepEqual(m.scopedModelIDs, tc.wantScope) {
						t.Errorf("setScopedModels = %v, want %v", m.scopedModelIDs, tc.wantScope)
					}
				}
			})
		})
	}
}
