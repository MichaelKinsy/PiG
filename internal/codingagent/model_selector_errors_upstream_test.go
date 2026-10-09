package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
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
		m.statusLine = NewFooterComponent(nil, "", nil)
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
			// The selector's own refresh reports the failed catalogs the registry's refresh returned, in the order they failed.
			ui := tui.NewWithOutput(io.Discard, 120, 40)
			applied := make(chan struct{}, 1)
			ui.SetOwnerDispatcher(func(_ context.Context, fn func()) error { fn(); applied <- struct{}{}; return nil })
			selector := tui.NewModelSelectorComponent(ui, nil, failedCatalogRuntime{registry: registry, result: result}, nil, func(*ai.Model) {}, func() {}, "", nil, nil)
			defer selector.Dispose()
			synctest.Wait()
			<-applied
			rendered := strings.Join(selector.Render(120), "\n")
			want := "Could not refresh 2 model catalogs (openai, anthropic); showing cached models."
			if !strings.Contains(rendered, want) {
				t.Fatalf("render=%q, want %q", rendered, want)
			}
			// test/parity/scenarios/model-runtime-store-catalog/14-model-selector-error-order.toml compares this rendered row with the row Pi's
			// selector renders (test/parity/testdata/model-selector-errors.mjs): ANSI stripped, trailing padding trimmed.
			for line := range strings.SplitSeq(stripANSI(rendered), "\n") {
				if strings.Contains(line, "Could not refresh") {
					fmt.Println("CATALOG-ERROR:" + strings.TrimRight(line, " "))
				}
			}
		})
	})
}

// failedCatalogRuntime is a ModelRuntime whose catalog refresh already ran: it reports the failures that refresh returned.
type failedCatalogRuntime struct {
	registry *ModelRegistry
	result   CatalogRefreshResult
}

func (r failedCatalogRuntime) GetAvailableSnapshot() []*ai.Model {
	return r.registry.GetAvailableModelData()
}
func (failedCatalogRuntime) GetModel(string, string) *ai.Model { return nil }
func (failedCatalogRuntime) GetError() string                  { return "" }
func (r failedCatalogRuntime) Refresh(context.Context, ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	return ai.ModelsRefreshResult{Errors: r.result.Errors, ErrorOrder: r.result.failedProviders()}
}

// model-catalog-refresh.ts:46 `refreshModelCatalogs(modelRuntime, signal)` and model-selector.ts:192: the interactive mode hands the selector a
// runtime whose refresh reports the failed catalogs in the order they failed (the order Pi's error Map iterates), and an aborted refresh as aborted.
func TestModelSelectorRuntimeRefreshReportsFailedCatalogsInSettlementOrder(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, `{"providers":{"openai":{"baseUrl":"https://one.invalid","oauth":"radius"},"anthropic":{"baseUrl":"https://two.invalid","oauth":"radius"}}}`, nil)
	synctest.Test(t, func(t *testing.T) {
		ordered := &catalogErrorOrderStore{InMemoryModelsStore: store, started: make(chan string, 2), release: map[string]chan struct{}{"openai": make(chan struct{}), "anthropic": make(chan struct{})}}
		registry.SetModelsStore(ordered)
		m, _ := newExtensionDialogProbeSized(t, 120, 40)
		m.runCtx = t.Context()
		runtime := modelSelectorRuntime{m: m, registry: registry}
		done := make(chan ai.ModelsRefreshResult, 1)
		go func() { done <- runtime.Refresh(t.Context()) }()
		<-ordered.started
		<-ordered.started
		close(ordered.release["openai"])
		synctest.Wait()
		close(ordered.release["anthropic"])
		result := <-done
		if !slices.Equal(result.ErrorOrder, []string{"openai", "anthropic"}) || len(result.Errors) != 2 || result.Aborted {
			t.Fatalf("Refresh = %+v, want the failures in settlement order, not aborted", result)
		}
	})
	t.Run("a refresh the context ended is aborted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		m, _ := newExtensionDialogProbeSized(t, 120, 40)
		if result := (modelSelectorRuntime{m: m, registry: registry}).Refresh(ctx); !result.Aborted {
			t.Fatalf("Refresh on an ended context = %+v, want Aborted", result)
		}
	})
}
