package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type interactiveCatalogStore struct {
	*ai.InMemoryModelsStore
	release      chan struct{}
	started      chan context.Context
	ignoreCancel bool
	readError    error
}

func (s *interactiveCatalogStore) Read(ctx context.Context, providerID string) (*ai.ModelsStoreEntry, error) {
	if providerID == "radius" {
		s.started <- ctx
		if s.ignoreCancel {
			<-s.release
		} else {
			select {
			case <-s.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if s.readError != nil {
			return nil, s.readError
		}
	}
	return s.InMemoryModelsStore.Read(ctx, providerID)
}

// ModelSelectorComponent renders its cached snapshot before waiting for refresh,
// and dispose cancels that wait. A blocked local catalog must not freeze Escape.
func TestModelPickerRefreshRendersSnapshotAndCancels(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, "", nil)
	synctest.Test(t, func(t *testing.T) {
		blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
		registry.SetModelsStore(blocked)
		var output bytes.Buffer
		m := NewInteractiveMode(InteractiveOptions{AgentDir: t.TempDir(), ModelRegistry: registry})
		m.runCtx = t.Context()
		m.tuiInst = tui.NewWithOutput(&output, 100, 40)
		m.statusLine = NewStatusLine(nil, "", nil)
		done := make(chan bool, 1)
		go func() {
			_, ok := m.buildSlashContext(t.Context()).PickModel("")
			done <- ok
		}()
		refreshCtx := <-blocked.started
		synctest.Wait()
		rendered := strings.Contains(output.String(), "Refreshing model catalogs…")
		input, _ := m.modalRoute()
		responsive := input != nil
		if !responsive {
			// Let the pre-fix synchronous refresh finish so the red test drains.
			close(blocked.release)
			synctest.Wait()
			input, _ = m.modalRoute()
		}
		input <- []byte("\x1b")
		if ok := <-done; ok {
			t.Error("Escape selected a model")
		}
		synctest.Wait()
		if responsive {
			close(blocked.release)
		}
		if !rendered || !responsive {
			t.Errorf("catalog refresh blocked the picker: initial status rendered=%v, input ready=%v", rendered, responsive)
		}
		if refreshCtx.Err() == nil {
			t.Error("closing the picker did not cancel its catalog operation")
		}
	})
}

// interactive-mode.ts:updateAvailableProviderCount counts distinct providers
// in the available snapshot, or just the scoped models when a scope exists.
func TestFooterProviderCountIncludesRadiusAndHonorsScope(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, `{"providers":{"radius-dev":{"oauth":"radius","baseUrl":"https://local.invalid"}}}`, map[string]ai.Credential{
		"radius-dev": {Type: ai.CredentialAPIKey, Key: "fake"},
	})
	entry := json.RawMessage(`{"id":"local","name":"Local","provider":"radius-dev","api":"pi-messages","baseUrl":"https://local.invalid/v1","input":["text"],"contextWindow":1000,"maxTokens":50}`)
	if err := store.Write(t.Context(), "radius-dev", ai.ModelsStoreEntry{Models: []json.RawMessage{entry}}); err != nil {
		t.Fatal(err)
	}
	registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{})
	m := NewInteractiveMode(InteractiveOptions{AgentDir: t.TempDir(), ModelRegistry: registry})
	m.statusLine = NewStatusLine(nil, "", nil)
	providers := map[string]bool{"radius-dev": true}
	reachable, authed := ReachableProviders(), AuthenticatedProviders(m.opts.AgentDir)
	for _, model := range ai.ListModels("") {
		if reachable[model.Provider] && authed[model.Provider] {
			providers[model.Provider] = true
		}
	}
	m.updateProviderInfo()
	if got := m.statusLine.ProviderCount(); got != len(providers) {
		t.Errorf("footer providers = %d, want available providers %v", got, providers)
	}
	m.scopedModelIDs = []string{"radius-dev/local"}
	m.updateProviderInfo()
	if got := m.statusLine.ProviderCount(); got != 1 {
		t.Errorf("scoped footer providers = %d, want only radius-dev", got)
	}
}

// These are the three cases in upstream model-catalog-refresh.test.ts, plus
// pre-abort, different runtimes, errors, and late settlement after abandonment.
func TestModelCatalogRefreshCoordinator(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	for _, cancelOne := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared_cancelOne=%t", cancelOne), func(t *testing.T) {
			registry, _, store := radiusTestRegistry(t, "", nil)
			synctest.Test(t, func(t *testing.T) {
				failure := errors.New("fake store failure")
				blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4), readError: failure}
				registry.SetModelsStore(blocked)
				firstCtx, cancel := context.WithCancel(t.Context())
				defer cancel()
				firstDone := make(chan error, 1)
				secondDone := make(chan error, 1)
				go func() {
					result, err := RefreshModelCatalogs(firstCtx, registry)
					if err == nil {
						err = result.Errors["radius"]
					}
					firstDone <- err
				}()
				operationCtx := <-blocked.started
				go func() {
					result, err := RefreshModelCatalogs(t.Context(), registry)
					if err == nil {
						err = result.Errors["radius"]
					}
					secondDone <- err
				}()
				synctest.Wait()
				if len(blocked.started) != 0 {
					t.Error("overlapping callers started another catalog read")
				}
				if cancelOne {
					cancel()
					if err := <-firstDone; !errors.Is(err, context.Canceled) {
						t.Errorf("first = %v", err)
					}
					if operationCtx.Err() != nil {
						t.Error("one waiter cancelled another waiter's operation")
					}
				}
				close(blocked.release)
				if !cancelOne {
					if err := <-firstDone; !errors.Is(err, failure) {
						t.Errorf("first result = %v", err)
					}
				}
				if err := <-secondDone; !errors.Is(err, failure) {
					t.Errorf("second result = %v", err)
				}
				synctest.Wait()
				modelCatalogRefreshes.mu.Lock()
				defer modelCatalogRefreshes.mu.Unlock()
				if modelCatalogRefreshes.activeByRuntime[registry] != nil {
					t.Error("settled refresh retains runtime")
				}
			})
		})
	}
	t.Run("abandoned_operation_does_not_remove_replacement", func(t *testing.T) {
		registry, _, store := radiusTestRegistry(t, "", nil)
		synctest.Test(t, func(t *testing.T) {
			old := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4), ignoreCancel: true}
			registry.SetModelsStore(old)
			ctx, cancel := context.WithCancel(t.Context())
			first := make(chan error, 1)
			go func() { _, err := RefreshModelCatalogs(ctx, registry); first <- err }()
			operationCtx := <-old.started
			cancel()
			if err := <-first; !errors.Is(err, context.Canceled) {
				t.Errorf("first = %v", err)
			}
			if operationCtx.Err() == nil {
				t.Error("abandoned operation not cancelled")
			}
			next := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
			registry.SetModelsStore(next)
			second := make(chan error, 1)
			go func() { _, err := RefreshModelCatalogs(t.Context(), registry); second <- err }()
			<-next.started
			close(old.release)
			synctest.Wait()
			third := make(chan error, 1)
			go func() { _, err := RefreshModelCatalogs(t.Context(), registry); third <- err }()
			synctest.Wait()
			if len(next.started) != 0 {
				t.Error("late settlement removed the replacement refresh")
			}
			close(next.release)
			if err := <-second; err != nil {
				t.Error(err)
			}
			if err := <-third; err != nil {
				t.Error(err)
			}
		})
	})
	t.Run("preaborted_and_distinct_runtimes", func(t *testing.T) {
		first, _, store1 := radiusTestRegistry(t, "", nil)
		second, _, store2 := radiusTestRegistry(t, "", nil)
		synctest.Test(t, func(t *testing.T) {
			one := &interactiveCatalogStore{InMemoryModelsStore: store1, release: make(chan struct{}), started: make(chan context.Context, 4)}
			two := &interactiveCatalogStore{InMemoryModelsStore: store2, release: make(chan struct{}), started: make(chan context.Context, 4)}
			first.SetModelsStore(one)
			second.SetModelsStore(two)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := RefreshModelCatalogs(ctx, first); !errors.Is(err, context.Canceled) {
				t.Errorf("pre-abort = %v", err)
			}
			if len(one.started) != 0 {
				t.Error("pre-aborted caller started work")
			}
			done := make(chan error, 2)
			go func() { _, err := RefreshModelCatalogs(t.Context(), first); done <- err }()
			go func() { _, err := RefreshModelCatalogs(t.Context(), second); done <- err }()
			<-one.started
			<-two.started
			close(one.release)
			close(two.release)
			for range 2 {
				if err := <-done; err != nil {
					t.Error(err)
				}
			}
		})
	})
}

func storedPickerModel(id, name string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%q,"name":%q,"provider":"radius","api":"pi-messages","baseUrl":"https://local.invalid/v1","input":["text"],"contextWindow":1000,"maxTokens":50}`, id, name))
}

func TestModelPickerRefreshUpdatesSnapshotAndPreservesScope(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	for _, query := range []string{"", "new"} {
		t.Run("query="+query, func(t *testing.T) {
			registry, _, store := radiusTestRegistry(t, "", map[string]ai.Credential{"radius": {Type: ai.CredentialAPIKey, Key: "fake"}})
			if err := store.Write(t.Context(), "radius", ai.ModelsStoreEntry{Models: []json.RawMessage{storedPickerModel("old", "Old snapshot")}}); err != nil {
				t.Fatal(err)
			}
			registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{})
			synctest.Test(t, func(t *testing.T) {
				blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
				registry.SetModelsStore(blocked)
				m, output := newExtensionDialogProbeSized(t, 100, 40)
				m.opts = InteractiveOptions{AgentDir: t.TempDir(), ModelRegistry: registry, Model: &ai.Model{ID: "old", ProviderMeta: ai.ProviderMetadata{ProviderID: "radius"}}}
				m.scopedModelIDs = []string{"radius/old"}
				m.statusLine = NewStatusLine(m.opts.Model, "", nil)
				done := make(chan string, 1)
				go func() { fq, _ := m.buildSlashContext(t.Context()).PickModel(""); done <- fq }()
				<-blocked.started
				synctest.Wait()
				input, _ := m.modalRoute()
				if query != "" {
					input <- []byte("\t") // switch to all before the refresh arrives
					input <- []byte(query)
				}
				if err := store.Write(t.Context(), "radius", ai.ModelsStoreEntry{Models: []json.RawMessage{storedPickerModel("old", "Refreshed current"), storedPickerModel("new", "New arrival")}}); err != nil {
					t.Fatal(err)
				}
				close(blocked.release)
				synctest.Wait()
				wantName, wantFQ := "Refreshed current", "radius/old"
				if query != "" {
					wantName, wantFQ = "New arrival", "radius/new"
				}
				if !strings.Contains(output.String(), wantName) || !strings.Contains(output.String(), "Model catalogs refreshed.") {
					t.Errorf("refreshed selector missing %q: %q", wantName, output.String())
					input <- []byte("\x1b")
				} else {
					input <- []byte("\r")
				}
				if got := <-done; got != wantFQ {
					t.Errorf("selection = %q, want %q", got, wantFQ)
				}
				if m.opts.Model.ID != "old" {
					t.Error("refresh switched the session model")
				}
			})
		})
	}
}

func TestModelPickerRefreshTimeoutAndErrors(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprintf("timeout=%t", timeout), func(t *testing.T) {
			registry, _, store := radiusTestRegistry(t, "", nil)
			synctest.Test(t, func(t *testing.T) {
				blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4), readError: errors.New("offline store failure")}
				registry.SetModelsStore(blocked)
				var output bytes.Buffer
				m := NewInteractiveMode(InteractiveOptions{AgentDir: t.TempDir(), ModelRegistry: registry})
				m.tuiInst = tui.NewWithOutput(&output, 100, 40)
				m.statusLine = NewStatusLine(nil, "", nil)
				done := make(chan bool, 1)
				go func() { _, ok := m.buildSlashContext(t.Context()).PickModel(""); done <- ok }()
				<-blocked.started
				synctest.Wait()
				want := "Could not refresh radius; showing cached models."
				if timeout {
					time.Sleep(15 * time.Second)
					want = "Model refresh timed out; showing cached models."
				} else {
					close(blocked.release)
				}
				synctest.Wait()
				if !strings.Contains(output.String(), want) {
					t.Errorf("missing %q: %q", want, output.String())
				}
				input, _ := m.modalRoute()
				input <- []byte("\x1b")
				<-done
			})
		})
	}
}
