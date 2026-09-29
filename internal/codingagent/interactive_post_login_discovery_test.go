package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestPostLoginModelSelectionRules(t *testing.T) {
	const action = "Logged in to Provider"
	for _, tc := range []struct {
		name, provider    string
		ids               []string
		selected, problem string
	}{
		{"oauth default", "github-copilot", []string{"other", "gpt-5.4"}, "gpt-5.4", ""},
		{"api key default", "openai", []string{"other", "gpt-5.5"}, "gpt-5.5", ""},
		{"no default", "custom", []string{"first"}, "", `, but no default model is configured for provider "custom". Use /model to select a model.`},
		{"empty no default", "custom", nil, "", `, but no default model is configured for provider "custom". Use /model to select a model.`},
		{"empty catalog", "openai", nil, "", ", but no models are available for that provider. Use /model to select a model."},
		{"missing default", "openai", []string{"other"}, "", `, but its default model "gpt-5.5" is not available. Use /model to select a model.`},
		{"radius default", "radius", []string{"fast", "balanced"}, "balanced", ""},
		{"radius catalog order", "radius", []string{"powerful", "fast"}, "powerful", ""},
		{"llama empty", "llama.cpp", nil, "", ". No llama.cpp models are loaded. Use /llama to load a model, then /model to select it."},
		{"llama loaded", "llama.cpp", []string{"local"}, "", ". Use /model to select a loaded llama.cpp model, or /llama to manage models."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models := []tui.ModelSelectorItem{{Provider: "other", ID: "gpt-5.5"}}
			for _, id := range tc.ids {
				models = append(models, tui.ModelSelectorItem{Provider: tc.provider, ID: id})
			}
			selected, problem := postLoginModel(tc.provider, action, DefaultModelPerProvider()[tc.provider], models)
			wantProblem := tc.problem
			if wantProblem != "" {
				wantProblem = action + wantProblem
			}
			if selected != tc.selected || problem != wantProblem {
				t.Fatalf("selection = %q, %q; want %q, %q", selected, problem, tc.selected, wantProblem)
			}
		})
	}
}

// Port of Pi test/suite/regressions/7027-credential-refresh-hang.test.ts:128-219.
// The real registry reads a blocked store; discovery, deadlines, and owner-loop delivery are deterministic.
func TestPostLoginModelDiscovery(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	for _, name := range []string{"prefer default", "catalog order", "external path", "empty", "preserve model", "preserve session", "timeout", "refresh error", "shutdown"} {
		t.Run(name, func(t *testing.T) {
			registry, _, store := radiusTestRegistry(t, `{"providers":{"radius":{"oauth":"radius","baseUrl":"https://local.invalid"}}}`, map[string]ai.Credential{"radius": {Type: ai.CredentialAPIKey, Key: "test-key"}})
			synctest.Test(t, func(t *testing.T) {
				m := newPostLoginTestMode(t)
				m.opts.ModelRegistry = registry
				blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
				registry.SetModelsStore(blocked)
				authPath := ""
				if name == "external path" {
					authPath = filepath.Join(m.opts.AgentDir, "external-store.json")
				}
				refreshStarted := time.Now()
				m.completeProviderAuthentication("radius", "Radius", ai.CredentialOAuth, nil, authPath, nil)
				refreshCtx := <-blocked.started
				synctest.Wait()
				if got := plainRender(m.chatContainer); !strings.Contains(got, "Refreshing model catalog…") || strings.Contains(got, "Error:") || m.opts.Model != nil {
					t.Fatalf("login did not defer: %s", got)
				}
				if name == "external path" && !strings.Contains(plainRender(m.chatContainer), authPath+". Refreshing model catalog…") {
					t.Fatal("deferred notice lost the external credential path")
				}
				switch name {
				case "preserve model":
					m.opts.Model = &ai.Model{ID: "chosen"}
				case "preserve session":
					m.opts.SessionHandle = &recordingCompactHandle{}
				case "timeout":
					// Pi awaits advanceTimersByTimeAsync(15_000), including the cancellation callback, before releasing the blocked store.
					<-refreshCtx.Done()
					if elapsed := time.Since(refreshStarted); elapsed != 15*time.Second || !errors.Is(refreshCtx.Err(), context.DeadlineExceeded) {
						t.Fatalf("refresh cancellation after %s: %v; want DeadlineExceeded at 15s", elapsed, refreshCtx.Err())
					}
				case "refresh error":
					blocked.readError = errors.New("store failed")
				case "shutdown":
					m.backgroundCancel()
				}
				ids := []string{"fast", "balanced"}
				if name == "catalog order" {
					ids = []string{"powerful", "fast"}
				}
				if name == "empty" || name == "timeout" {
					ids = nil
				}
				var models []json.RawMessage
				for _, id := range ids {
					models = append(models, json.RawMessage(fmt.Sprintf(`{"id":%q,"name":%q,"provider":"radius","api":"pi-messages","baseUrl":"https://local.invalid/v1","input":["text"],"contextWindow":1000,"maxTokens":100}`, id, id)))
				}
				if err := store.Write(t.Context(), "radius", ai.ModelsStoreEntry{Models: models}); err != nil {
					t.Fatal(err)
				}
				close(blocked.release)
				drainPostLoginTasks(m)
				got := plainRender(m.chatContainer)
				if name == "external path" && (!strings.Contains(got, "Selected balanced. Credentials saved to "+authPath) || strings.Contains(got, "auth.json")) {
					t.Fatal("deferred completion reported the wrong store")
				}
				switch name {
				case "prefer default", "catalog order", "external path":
					want := "balanced"
					if name == "catalog order" {
						want = "powerful"
					}
					if modelSpec(m.opts.Model) != "radius/"+want || m.opts.SettingsManager.GetDefaultModel() != want {
						t.Fatalf("model/default = %s/%s", modelSpec(m.opts.Model), m.opts.SettingsManager.GetDefaultModel())
					}
				case "preserve model", "preserve session", "shutdown":
					if strings.Contains(got, "Selected") || strings.Contains(got, "Error:") {
						t.Fatalf("late completion changed selection: %s", got)
					}
				case "empty", "timeout", "refresh error":
					if !strings.Contains(got, "no models are available for that provider") {
						t.Fatalf("missing deferred error: %s", got)
					}
				}
				if name == "timeout" && (refreshCtx.Err() == nil || !strings.Contains(got, "model catalog refresh timed out; using cached models.")) {
					t.Fatalf("deadline not reported: %s", got)
				}
				if name == "refresh error" && !strings.Contains(got, "model catalog could not be refreshed; using cached models.") {
					t.Fatalf("missing warning: %s", got)
				}
				if name == "shutdown" && refreshCtx.Err() == nil {
					t.Fatal("shutdown did not cancel refresh")
				}
			})
		})
	}
}

func drainPostLoginTasks(m *InteractiveMode) {
	for {
		synctest.Wait()
		select {
		case task := <-m.uiTaskCh:
			task()
		default:
			return
		}
	}
}

// Pi's #7027 regression completes a known-model login without awaiting the catalog network operation.
func TestPostLoginCompletesBeforeBackgroundRefresh(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	registry, _, store := radiusTestRegistry(t, `{"providers":{"radius":{"oauth":"radius","baseUrl":"https://local.invalid"}}}`, nil)
	synctest.Test(t, func(t *testing.T) {
		m := newPostLoginTestMode(t)
		m.opts.ModelRegistry = registry
		previous := &ai.Model{ID: "chosen"}
		m.opts.Model = previous
		blocked := &interactiveCatalogStore{InMemoryModelsStore: store, release: make(chan struct{}), started: make(chan context.Context, 4)}
		registry.SetModelsStore(blocked)
		before := time.Now()
		m.completeProviderAuthentication("radius", "Radius", ai.CredentialAPIKey, previous, "", nil)
		refreshCtx := <-blocked.started
		synctest.Wait()
		if time.Since(before) != 0 {
			t.Fatal("login awaited the catalog deadline")
		}
		got := plainRender(m.chatContainer)
		if !strings.Contains(got, "Saved API key for Radius. Credentials saved to") || strings.Contains(got, "Warning:") {
			t.Fatalf("initial status = %s", got)
		}
		<-refreshCtx.Done()
		if elapsed := time.Since(before); elapsed != 15*time.Second || !errors.Is(refreshCtx.Err(), context.DeadlineExceeded) {
			t.Fatalf("refresh cancellation after %s: %v; want DeadlineExceeded at 15s", elapsed, refreshCtx.Err())
		}
		drainPostLoginTasks(m)
		close(blocked.release)
		got = plainRender(m.chatContainer)
		if !strings.Contains(got, "model catalog refresh timed out; using cached models.") || strings.Contains(got, "Error:") || m.opts.Model != previous {
			t.Fatalf("completion = %s", got)
		}
	})
}

func TestPostLoginPreservesKnownModelAndReportsSelectionFailure(t *testing.T) {
	for _, name := range []string{"known", "builder failure", "persistence failure", "unknown sentinel"} {
		t.Run(name, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			previous := &ai.Model{ID: "my-custom-id", ProviderMeta: ai.ProviderMetadata{ProviderID: "openai"}}
			if name != "known" {
				previous = nil
			}
			if name == "unknown sentinel" {
				previous = &ai.Model{ID: "unknown", ProviderMeta: ai.ProviderMetadata{ProviderID: "unknown", API: "unknown"}}
			}
			m.opts.Model = previous
			if name == "builder failure" {
				m.opts.ModelBuilder = func(string) (*ai.Model, error) { return nil, errors.New("cannot build") }
			}
			if name == "persistence failure" {
				m.opts.SessionHandle = &postLoginFailingHandle{}
			}
			if err := setPostLoginAPIKey(m, "openai", "test-key"); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "known":
				if m.opts.Model != previous || m.opts.SettingsManager.GetDefaultModel() != "" {
					t.Fatal("known model replaced or persisted")
				}
				if got := plainRender(m.chatContainer); strings.Contains(got, "Selected") || strings.Contains(got, "Error:") {
					t.Fatal(got)
				}
			case "unknown sentinel":
				waitPostLoginStatus(t, m, "Selected gpt-5.5.")
			default:
				reason := "cannot build"
				if name == "persistence failure" {
					reason = "cannot persist"
				}
				waitPostLoginStatus(t, m, "Saved API key for OpenAI, but selecting its default model failed: "+reason+". Use /model to select a model.")
				if got := plainRender(m.chatContainer); strings.Contains(got, "Selected") {
					t.Fatal(got)
				}
			}
		})
	}
}

func BenchmarkPostLoginModelSelection(b *testing.B) {
	var models []tui.ModelSelectorItem
	for _, model := range ai.ListModels("") {
		models = append(models, tui.ModelSelectorItem{Provider: model.Provider, ID: model.ID})
	}
	b.ReportAllocs()
	for b.Loop() {
		selected, problem := postLoginModel("openai", "Saved API key for OpenAI", DefaultModelPerProvider()["openai"], models)
		if selected != "gpt-5.5" || problem != "" {
			b.Fatalf("selection = %s, %s", selected, problem)
		}
	}
}

func TestPostLoginPersistsDefaultIntoNonEmptyScope(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.scopedModelIDs = []string{"openai/other"}
	if err := m.opts.SettingsManager.UpdateGlobal(func(s *Settings) { s.EnabledModels = []string{"openai/other"} }); err != nil {
		t.Fatal(err)
	}
	if err := setPostLoginAPIKey(m, "openai", "test-key"); err != nil {
		t.Fatal(err)
	}
	waitPostLoginStatus(t, m, "Selected gpt-5.5.")
	if got := strings.Join(m.scopedModelIDs, ","); got != "openai/other,openai/gpt-5.5" {
		t.Fatalf("scope = %s", got)
	}
	if got := strings.Join(m.opts.SettingsManager.GetEnabledModels(), ","); got != "openai/other,openai/gpt-5.5" {
		t.Fatalf("saved scope = %s", got)
	}
}

type postLoginFailingHandle struct{ recordingCompactHandle }

func (*postLoginFailingHandle) SetModelOnMain(_ *ai.Model, _ ModelMutationOptions, dispatch func(func() error) error) error {
	return dispatch(func() error { return errors.New("cannot persist") })
}
