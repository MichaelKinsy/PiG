package codingagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func modelCommandProbe(t *testing.T) (*InteractiveMode, *ai.InMemoryModelsStore, *interactiveCatalogStore) {
	t.Helper()
	registry, _, store := radiusTestRegistry(t, "", map[string]ai.Credential{"radius": {Type: ai.CredentialAPIKey, Key: "key"}})
	if err := store.Write(t.Context(), "radius", ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{storedPickerModel("cached", "Cached")})}); err != nil {
		t.Fatal(err)
	}
	registry.RefreshCatalogs(t.Context(), CatalogRefreshOptions{})
	blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
	registry.SetModelsStore(blocked)
	m, _ := newExtensionDialogProbeSized(t, 100, 40)
	m.opts.ModelRegistry = registry
	m.opts.AgentDir = registry.agentDir
	m.runCtx = t.Context()
	m.statusLine = NewFooterComponent(nil, "", nil)
	m.slashRegistry = NewSlashRegistry()
	m.opts.ModelBuilder = func(spec string) (*ai.Model, error) {
		_, id, _ := strings.Cut(spec, "/")
		return &ai.Model{ID: id, DisplayName: id, ProviderMeta: ai.ProviderMetadata{ProviderID: "radius"}}, nil
	}
	return m, store, blocked
}

func TestModelCommandCachedMatchUpstream7443(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7443-model-command-cached-match.test.ts:23
	t.Run("matches the availability snapshot without starting a catalog refresh", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, _, blocked := modelCommandProbe(t)
			called := false
			m.findExactModelMatch(t.Context(), "cached", func(spec string, ok bool) {
				called = true
				if !ok || spec != "radius/cached" {
					t.Errorf("match=%q, %v", spec, ok)
				}
			})
			if !called {
				t.Fatal("cached match waited for refresh")
			}
			if len(blocked.started) != 0 {
				t.Fatal("cached match started refresh")
			}
			if text := strings.Join(m.chatContainer.Render(100), "\n"); strings.TrimSpace(text) != "" {
				t.Fatalf("cached match showed status: %q", text)
			}
		})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7443-model-command-cached-match.test.ts:37
	t.Run("uses a caller-owned deadline only after a cache miss", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			m, _, blocked := modelCommandProbe(t)
			called := false
			started := time.Now()
			m.findExactModelMatch(t.Context(), "not-cached", func(spec string, ok bool) {
				called = true
				if ok || spec != "" {
					t.Errorf("match=%q, %v", spec, ok)
				}
			})
			signal := <-blocked.started
			if signal == nil || signal.Done() == nil {
				t.Fatal("refresh has no cancellation signal")
			}
			if text := strings.Join(m.chatContainer.Render(100), "\n"); !strings.Contains(text, "Refreshing model catalogs…") {
				t.Fatalf("status=%q", text)
			}
			// Drive the real caller-owned deadline instead of mocking an already-aborted refresh result.
			m.backgroundTasks.Wait()
			if time.Since(started) != 15*time.Second {
				t.Fatalf("deadline elapsed=%v", time.Since(started))
			}
			if len(blocked.started) != 0 {
				t.Fatal("more than one refresh started")
			}
			if called {
				t.Fatal("worker completed on the wrong executor")
			}
			(<-m.uiTaskCh)()
			if !called {
				t.Fatal("miss did not resolve")
			}
			if text := strings.Join(m.chatContainer.Render(100), "\n"); !strings.Contains(text, "Model refresh timed out; searching cached models.") {
				t.Fatalf("warning=%q", text)
			}
		})
	})
}

func TestModelCommandScopedMissDoesNotRefresh(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	synctest.Test(t, func(t *testing.T) {
		m, _, blocked := modelCommandProbe(t)
		m.scopedModelIDs = []string{"radius/unavailable"}
		called := false
		m.findExactModelMatch(t.Context(), "cached", func(spec string, ok bool) {
			called = true
			if ok || spec != "" {
				t.Errorf("out-of-scope match=%q, %v", spec, ok)
			}
		})
		if !called || len(blocked.started) != 0 {
			t.Fatal("scoped miss refreshed")
		}
	})
}

func TestModelCommandCompletionDoesNotOutliveOwner(t *testing.T) {
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	for _, phase := range []string{"refreshing", "queued", "backpressure"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, _, blocked := modelCommandProbe(t)
				owner, cancel := context.WithCancel(t.Context())
				defer cancel()
				m.backgroundCtx = owner
				called := false
				if phase == "backpressure" {
					for range cap(m.uiTaskCh) {
						m.uiTaskCh <- func() {}
					}
				}
				m.findExactModelMatch(t.Context(), "not-cached", func(string, bool) { called = true })
				<-blocked.started
				if phase != "refreshing" {
					close(blocked.release)
				}
				if phase == "queued" {
					m.backgroundTasks.Wait()
				} else {
					synctest.Wait()
				}
				cancel()
				m.backgroundTasks.Wait()
				for len(m.uiTaskCh) > 0 {
					(<-m.uiTaskCh)()
				}
				if called {
					t.Fatal("completion outlived the owner")
				}
			})
		})
	}
}

func TestModelCommandRefreshDoesNotBlockOwnerLoop(t *testing.T) {
	// interactive-mode.ts:5039-5096 awaits catalog refresh before deciding to open the picker.
	clearAllAuthEnv(t)
	t.Setenv("PI_OFFLINE", "1")
	synctest.Test(t, func(t *testing.T) {
		m, store, blocked := modelCommandProbe(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		returned := make(chan struct{})
		go func() { m.dispatchSlash(ctx, "/model refreshed"); close(returned) }()
		<-blocked.started
		synctest.Wait()
		select {
		case <-returned:
		default:
			cancel()
			<-returned
			t.Fatal("catalog miss blocks the owner loop inside the picker")
		}
		if _, ok := m.resolveAvailableModel("refreshed"); ok {
			t.Fatal("model existed before refresh")
		}
		if err := store.Write(t.Context(), "radius", ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{storedPickerModel("cached", "Cached"), storedPickerModel("refreshed", "Refreshed")})}); err != nil {
			t.Fatal(err)
		}
		close(blocked.release)
		m.backgroundTasks.Wait()
		if m.opts.Model != nil {
			t.Fatal("worker changed model before owner accepted completion")
		}
		select {
		case task := <-m.uiTaskCh:
			task()
		default:
			t.Fatal("missing owner-loop completion")
		}
		if m.opts.Model == nil || m.opts.Model.ID != "refreshed" {
			t.Fatalf("selected=%+v", m.opts.Model)
		}
		if input, _ := m.modalRoute(); input != nil {
			t.Fatal("exact refreshed match opened a picker")
		}
	})
}
