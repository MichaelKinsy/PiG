package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestScopedModelsRefreshUpstream7153(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	for _, tc := range []struct {
		name   string
		cancel bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7153-scoped-models-refresh.test.ts:76
		{"renders cached models immediately and updates after background refresh", false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7153-scoped-models-refresh.test.ts:98
		{"cancels the background refresh when the selector closes", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, _, store := radiusTestRegistry(t, `{"providers":{"radius":{"baseUrl":"https://catalog.example.test","oauth":"radius"}}}`, map[string]ai.Credential{"radius": {Type: ai.CredentialAPIKey, Key: "test-key"}})
			if err := store.Write(t.Context(), "radius", ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{storedPickerModel("cached", "Cached")})}); err != nil {
				t.Fatal(err)
			}
			registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{})
			synctest.Test(t, func(t *testing.T) {
				blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
				registry.SetModelsStore(blocked)
				m, _ := newExtensionDialogProbeSized(t, 100, 40)
				m.opts.ModelRegistry = registry
				m.opts.AgentDir = registry.agentDir
				m.runCtx = t.Context()
				m.statusLine = NewFooterComponent(nil, "", nil)
				done := make(chan struct{})
				go func() { m.showScopedModels(); close(done) }()
				signal := <-blocked.started
				synctest.Wait()
				rendered := strings.Join(m.editorContainer.Render(100), "\n")
				if !strings.Contains(rendered, "cached") || !strings.Contains(rendered, "Refreshing model catalogs…") || strings.Contains(rendered, "refreshed") {
					t.Errorf("initial selector = %q", rendered)
				}
				input, _ := m.modalRoute()
				if !tc.cancel {
					if err := store.Write(t.Context(), "radius", ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{storedPickerModel("cached", "Cached"), storedPickerModel("refreshed", "Refreshed")})}); err != nil {
						t.Fatal(err)
					}
					close(blocked.release)
					synctest.Wait()
					rendered = strings.Join(m.editorContainer.Render(100), "\n")
					if !strings.Contains(rendered, "refreshed") || !strings.Contains(rendered, "Model catalogs refreshed.") {
						t.Errorf("refreshed selector = %q", rendered)
					}
				}
				input <- []byte("\x1b")
				<-done
				if tc.cancel {
					synctest.Wait()
					if signal.Err() == nil {
						t.Error("closing selector did not cancel refresh")
					}
					close(blocked.release)
				}
			})
		})
	}
}

func TestScopedModelsUsesAvailableSnapshot(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/modes/interactive/interactive-mode.ts:5219-5221
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("GEMINI_API_KEY", "test-key")
	synctest.Test(t, func(t *testing.T) {
		m, _ := newExtensionDialogProbeSized(t, 100, 40)
		m.opts.AgentDir = t.TempDir()
		m.opts.ModelRegistry = NewModelRegistry(m.opts.AgentDir)
		m.opts.ModelRegistry.SetModelsStore(ai.NewInMemoryModelsStore())
		m.runCtx = t.Context()
		m.statusLine = NewFooterComponent(nil, "", nil)
		done := make(chan struct{})
		go func() { m.showScopedModels(); close(done) }()
		synctest.Wait()
		rendered := strings.Join(m.editorContainer.Render(100), "\n")
		if !strings.Contains(rendered, "gemini") {
			t.Errorf("configured Google catalog missing: %q", rendered)
		}
		if input, _ := m.modalRoute(); input != nil {
			input <- []byte("\x1b")
		}
		<-done
	})
}

func TestModelsJSONHotReloadUpstream6999(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6999-models-json-hot-reload.test.ts:59
	t.Run("reloads models.json when opening /model", func(t *testing.T) {
		clearAllAuthEnv(t)
		t.Setenv("PI_OFFLINE", "1")
		dir := t.TempDir()
		write := func(provider, model string) {
			t.Helper()
			data := fmt.Sprintf(`{"providers":{%q:{"baseUrl":"https://example.test/v1","api":"openai-completions","apiKey":"test-key","models":[{"id":%q}]}}}`, provider, model)
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		write("old-provider", "old-model")
		registry := NewModelRegistry(dir)
		registry.SetModelsStore(ai.NewInMemoryModelsStore())
		if _, ok := registry.Resolve("old-provider", "old-model"); !ok {
			t.Fatal("initial model missing")
		}
		write("new-provider", "new-model")
		synctest.Test(t, func(t *testing.T) {
			m, _ := newExtensionDialogProbeSized(t, 120, 40)
			m.opts.AgentDir = dir
			m.opts.ModelRegistry = registry
			m.runCtx = t.Context()
			m.statusLine = NewFooterComponent(nil, "", nil)
			done := make(chan struct{})
			go func() { m.pickModel(t.Context(), ""); close(done) }()
			synctest.Wait()
			rendered := strings.Join(m.editorContainer.Render(120), "\n")
			if !strings.Contains(rendered, "new-model") || !strings.Contains(rendered, "[new-provider]") || strings.Contains(rendered, "old-model") {
				t.Errorf("selector = %q", rendered)
			}
			input, _ := m.modalRoute()
			input <- []byte("\x1b")
			<-done
		})
	})
}
