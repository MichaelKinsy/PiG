package codingagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type catalogErrorOrderStore struct {
	*ai.InMemoryModelsStore
	started chan string
	release map[string]chan struct{}
}

func (s *catalogErrorOrderStore) Read(ctx context.Context, id string) (*ai.ModelsStoreEntry, error) {
	if release := s.release[id]; release != nil {
		s.started <- id
		select {
		case <-release:
			return nil, errors.New("unavailable")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.InMemoryModelsStore.Read(ctx, id)
}

func TestScopedSelectorFailedCatalogOrder(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/modes/interactive/interactive-mode.ts:5315-5320
	clearAllAuthEnv(t)
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, `{"providers":{"openai":{"baseUrl":"https://one.invalid","oauth":"radius"},"anthropic":{"baseUrl":"https://two.invalid","oauth":"radius"}}}`, nil)
	synctest.Test(t, func(t *testing.T) {
		ordered := &catalogErrorOrderStore{InMemoryModelsStore: store, started: make(chan string, 2), release: map[string]chan struct{}{"openai": make(chan struct{}), "anthropic": make(chan struct{})}}
		registry.SetModelsStore(ordered)
		m, _ := newExtensionDialogProbeSized(t, 120, 40)
		m.opts.AgentDir, m.opts.ModelRegistry, m.runCtx = registry.agentDir, registry, t.Context()
		m.statusLine = NewStatusLine(nil, "", nil)
		done := make(chan struct{})
		go func() { m.showScopedModels(); close(done) }()
		<-ordered.started
		<-ordered.started
		close(ordered.release["openai"])
		synctest.Wait()
		close(ordered.release["anthropic"])
		synctest.Wait()
		rendered := strings.Join(m.editorContainer.Render(120), "\n")
		if !strings.Contains(rendered, "Could not refresh openai, anthropic; showing cached models.") {
			t.Errorf("scoped selector diagnostic = %q", rendered)
		}
		input, _ := m.modalRoute()
		input <- []byte("\x1b")
		<-done
	})
}

func TestModelSelectorListsEveryFailedCatalogUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/model-selector.test.ts:91
	t.Run("lists every catalog that failed to refresh", func(t *testing.T) {
		registry, _, store := radiusTestRegistry(t, `{"providers":{"openai":{"baseUrl":"https://one.invalid","oauth":"radius"},"anthropic":{"baseUrl":"https://two.invalid","oauth":"radius"}}}`, nil)
		synctest.Test(t, func(t *testing.T) {
			ordered := &catalogErrorOrderStore{InMemoryModelsStore: store, started: make(chan string, 2), release: map[string]chan struct{}{"openai": make(chan struct{}), "anthropic": make(chan struct{})}}
			registry.SetModelsStore(ordered)
			done := make(chan CatalogRefreshResult, 1)
			go func() { done <- registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{}) }()
			<-ordered.started
			<-ordered.started
			close(ordered.release["openai"])
			synctest.Wait()
			close(ordered.release["anthropic"])
			result := <-done
			if len(result.Errors) != 2 {
				t.Fatalf("errors=%v", result.Errors)
			}
			selector := tui.NewModelSelector("Select model", nil, []tui.ModelSelectorItem{{Provider: "test", ID: "cached"}}, "")
			selector.SetError(modelCatalogRefreshError(result, nil))
			fmt.Println("CATALOG-ERROR:" + modelCatalogRefreshError(result, nil))
			rendered := strings.Join(selector.Render(120), "\n")
			want := "Could not refresh 2 model catalogs (openai, anthropic); showing cached models."
			if !strings.Contains(rendered, want) {
				t.Fatalf("render=%q, want %q", rendered, want)
			}
		})
	})
}
