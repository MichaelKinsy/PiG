package codingagent

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func drainIntegratedCatalogTasks(m *InteractiveMode) {
	m.backgroundTasks.Wait()
	for len(m.uiTaskCh) > 0 {
		(<-m.uiTaskCh)()
		m.backgroundTasks.Wait()
	}
}

// Pi 0.87.1 interactive-mode.ts:5879-5979 selects and persists the provider
// default after login when startup could not select a model.
func TestIntegratedAPIKeyLoginSelectsProviderDefault(t *testing.T) {
	m := newPostLoginTestMode(t)
	if err := setPostLoginAPIKey(m, "opencode-go", "fake-key"); err != nil {
		t.Fatal(err)
	}
	waitPostLoginStatus(t, m, "Selected kimi-k3")
	if got := modelSpec(m.opts.Model); got != "opencode-go/kimi-k3" {
		t.Fatalf("login left model %q, want opencode-go/kimi-k3", got)
	}
	saved := NewSettingsManager(t.TempDir(), m.opts.AgentDir)
	if got := saved.GetDefaultProvider() + "/" + saved.GetDefaultModel(); got != "opencode-go/kimi-k3" {
		t.Fatalf("saved default = %q", got)
	}
	want := "Saved API key for OpenCode Go. Selected kimi-k3. Credentials saved to " + filepath.Join(m.opts.AgentDir, "auth.json")
	if got := plainRender(m.chatContainer); !strings.Contains(got, want) {
		t.Fatalf("chat = %q, want %q", got, want)
	}
}

// Every decision and exact diagnostic comes from interactive-mode.ts:5900-5924.
func TestPostLoginModelBranches(t *testing.T) {
	for _, tc := range []struct {
		name, provider, defaultID string
		models                    []string
		wantID, wantError         string
	}{
		{name: "default", provider: "opencode-go", defaultID: "kimi-k2.6", models: []string{"other", "kimi-k2.6"}, wantID: "kimi-k2.6"},
		{name: "no default", provider: "custom", models: []string{"first"}, wantError: `Saved API key for Provider, but no default model is configured for provider "custom". Use /model to select a model.`},
		{name: "no models", provider: "opencode-go", defaultID: "kimi-k2.6", wantError: "Saved API key for Provider, but no models are available for that provider. Use /model to select a model."},
		{name: "missing default", provider: "opencode-go", defaultID: "kimi-k2.6", models: []string{"other"}, wantError: `Saved API key for Provider, but its default model "kimi-k2.6" is not available. Use /model to select a model.`},
		{name: "radius prefers balanced", provider: "radius", defaultID: "balanced", models: []string{"z-first", "balanced"}, wantID: "balanced"},
		{name: "radius catalog order", provider: "radius", defaultID: "balanced", models: []string{"z-first", "a-last"}, wantID: "z-first"},
		{name: "llama empty", provider: "llama.cpp", wantError: "Saved API key for Provider. No llama.cpp models are loaded. Use /llama to load a model, then /model to select it."},
		{name: "llama loaded", provider: "llama.cpp", models: []string{"loaded"}, wantError: "Saved API key for Provider. Use /model to select a loaded llama.cpp model, or /llama to manage models."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models := []tui.ModelSelectorItem{{Provider: "unrelated", ID: tc.defaultID}}
			for _, id := range tc.models {
				models = append(models, tui.ModelSelectorItem{Provider: tc.provider, ID: id})
			}
			before := slices.Clone(models)
			id, message := postLoginModel(tc.provider, "Saved API key for Provider", tc.defaultID, models)
			if id != tc.wantID || message != tc.wantError {
				t.Fatalf("got %q, %q; want %q, %q", id, message, tc.wantID, tc.wantError)
			}
			if !slices.Equal(before, models) {
				t.Fatal("mutated catalog snapshot")
			}
		})
	}
}

func TestPostLoginKeepsSelectedModel(t *testing.T) {
	t.Setenv("PIG_OFFLINE", "1")
	for _, provider := range []string{"opencode-go", "radius", "custom", "llama.cpp"} {
		t.Run(provider, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			previous := &ai.Model{ID: "already-selected"}
			m.opts.Model = previous
			m.opts.ModelBuilder = func(string) (*ai.Model, error) {
				t.Error("replaced selected model")
				return nil, errors.New("unexpected")
			}
			m.completeProviderAuthentication(provider, "Provider", "api_key", previous, "", nil)
			drainIntegratedCatalogTasks(m)
			if m.opts.Model != previous {
				t.Fatal("model changed")
			}
			if got := plainRender(m.chatContainer); strings.Contains(got, "Error:") || strings.Contains(got, "Refreshing") || !strings.Contains(got, "Saved API key for Provider. Credentials saved to ") {
				t.Fatalf("chat = %q", got)
			}
			if m.opts.SettingsManager.GetDefaultModel() != "" {
				t.Fatal("saved a default for existing model")
			}
		})
	}
}

type postLoginModelHandle struct {
	*recordingCompactHandle
	settings  *SettingsManager
	err       error
	persisted bool
}

func (h *postLoginModelHandle) SetModelOnMain(model *ai.Model, options ModelMutationOptions, dispatch func(func() error) error) error {
	return dispatch(func() error { return h.SetModel(model, options) })
}

func (h *postLoginModelHandle) SetModel(model *ai.Model, options ...ModelMutationOptions) error {
	h.persisted = len(options) > 0 && options[0].Persist
	if h.err != nil {
		return h.err
	}
	h.agent.SetModel(model)
	h.agent.SetThinkingLevel(ai.ThinkingHigh)
	h.appendPersistedDefaultToScope(model, options)
	if h.persisted {
		if err := h.settings.SetDefaultModelAndProvider(model.ProviderMeta.ProviderID, model.ID); err != nil {
			return err
		}
		// Session.SetModel also extends a nonempty enabledModels setting.
		if enabled := h.settings.GetEnabledModels(); len(enabled) > 0 {
			spec := model.ProviderMeta.ProviderID + "/" + model.ID
			return h.settings.UpdateGlobal(func(s *Settings) { s.EnabledModels = append(enabled, spec) })
		}
	}
	return nil
}

func TestPostLoginOAuthUsesSessionPersistenceAndThinking(t *testing.T) {
	for _, provider := range []string{"anthropic", "github-copilot", "openai-codex"} {
		t.Run(provider, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			handle := &postLoginModelHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent}, settings: m.opts.SettingsManager}
			m.opts.SessionHandle = handle
			if err := handle.settings.UpdateGlobal(func(s *Settings) { s.EnabledModels = []string{"other/model"}; s.DefaultThinkingLevel = "low" }); err != nil {
				t.Fatal(err)
			}
			m.scopedModelIDs = []string{"other/model"}
			handle.scopedModels = []extension.ScopedModel{{Model: &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: "other"}}}}
			auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := auth.Set(provider, ai.Credential{Type: ai.CredentialOAuth, Access: "fake-access"}); err != nil {
				t.Fatal(err)
			}
			previous := &ai.Model{ID: "unknown", ProviderMeta: ai.ProviderMetadata{ProviderID: "unknown", API: "unknown"}}
			m.opts.Model = previous
			m.completeProviderAuthentication(provider, "Provider", "oauth", previous, "", nil)
			waitPostLoginStatus(t, m, "Logged in to Provider. Selected")
			if !handle.persisted {
				t.Fatal("Session.SetModel did not receive Persist")
			}
			want := m.opts.DefaultModelPerProvider[provider]
			if m.opts.Model == nil || m.opts.Model.ID != want || m.opts.SettingsManager.GetDefaultModel() != want {
				t.Fatalf("default selection = %v", m.opts.Model)
			}
			if m.thinkingLevel != "high" || m.editor.ThinkingLevel != "high" || handle.settings.GetDefaultThinkingLevel() != "low" {
				t.Fatalf("thinking level %q, editor %q, default %q", m.thinkingLevel, m.editor.ThinkingLevel, handle.settings.GetDefaultThinkingLevel())
			}
			if !slices.Contains(m.scopedModelIDs, provider+"/"+want) || !slices.Contains(handle.settings.GetEnabledModels(), provider+"/"+want) {
				t.Fatal("default missing from nonempty scope")
			}
			if got := plainRender(m.chatContainer); !strings.Contains(got, "Logged in to Provider. Selected "+want+". Credentials saved to ") {
				t.Fatalf("chat = %q", got)
			}
		})
	}
}

func TestPostLoginSelectionFailuresAreReported(t *testing.T) {
	for _, failure := range []string{"builder", "session", "settings"} {
		t.Run(failure, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			switch failure {
			case "builder":
				m.opts.ModelBuilder = func(string) (*ai.Model, error) { return nil, errors.New("build failed") }
			case "session":
				m.opts.SessionHandle = &postLoginModelHandle{recordingCompactHandle: &recordingCompactHandle{}, err: errors.New("session failed")}
			case "settings":
				if err := os.Mkdir(filepath.Join(m.opts.AgentDir, "settings.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := setPostLoginAPIKey(m, "opencode-go", "fake-key"); err != nil {
				t.Fatal(err)
			}
			waitPostLoginStatus(t, m, "selecting its default model failed:")
			got := plainRender(m.chatContainer)
			if !strings.Contains(got, "Saved API key for OpenCode Go. Credentials saved to ") || !strings.Contains(got, "Saved API key for OpenCode Go, but selecting its default model failed:") || !strings.Contains(got, "Use /model to select a model.") || strings.Contains(got, ". Selected ") {
				t.Fatalf("chat = %q", got)
			}
		})
	}
}

// Pi defers missing defaults until refresh and checks Session and model identity
// before finishing (interactive-mode.ts:5889-5894,5961-5963).
func TestPostLoginDeferredCatalog(t *testing.T) {
	for _, change := range []string{"none", "model", "session", "failure", "cancel"} {
		t.Run(change, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			t.Setenv("PIG_OFFLINE", "")
			t.Setenv("PI_OFFLINE", "")
			if err := os.Unsetenv("PI_OFFLINE"); err != nil {
				t.Fatal(err)
			}
			requested, release := make(chan struct{}), make(chan struct{})
			gateway := newRadiusTestGateway(t, func(w http.ResponseWriter, r *http.Request, origin string) {
				close(requested)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if change == "failure" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				serveRadiusConfig(w, r, origin)
			})
			registry, auth, _ := radiusTestRegistry(t, `{"providers":{"radius":{"baseUrl":"`+gateway.server.URL+`","oauth":"radius"}}}`, nil)
			m.opts.ModelRegistry = registry
			m.opts.AgentDir = filepath.Dir(auth.Path())
			handle := &postLoginModelHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent, inner: &Session{}}, settings: m.opts.SettingsManager}
			m.opts.SessionHandle = handle
			m.backgroundCtx, m.backgroundCancel = context.WithCancel(t.Context())
			t.Cleanup(m.backgroundCancel)
			if err := setPostLoginAPIKey(m, "radius", "fake-key"); err != nil {
				t.Fatal(err)
			}
			<-requested
			if m.opts.Model != nil || !strings.Contains(plainRender(m.chatContainer), "Refreshing model catalog…") {
				t.Fatal("selected before refresh completed")
			}
			var selected *ai.Model
			if change == "model" {
				selected = &ai.Model{ID: "user-choice"}
				m.opts.Model = selected
			}
			if change == "session" {
				handle.inner = &Session{}
			}
			if change == "cancel" {
				m.backgroundCancel()
			}
			close(release)
			switch change {
			case "none":
				waitPostLoginStatus(t, m, "Selected auto")
			case "failure":
				waitPostLoginStatus(t, m, "no models are available for that provider")
			default:
				m.opts.ModelBuilder = func(string) (*ai.Model, error) {
					t.Error("superseded refresh built a model")
					return nil, errors.New("superseded")
				}
				drainIntegratedCatalogTasks(m)
			}
			switch change {
			case "none":
				if got := modelSpec(m.opts.Model); got != "radius/auto" {
					t.Fatalf("catalog-order fallback = %q", got)
				}
			case "failure":
				got := plainRender(m.chatContainer)
				if !strings.Contains(got, "model catalog could not be refreshed; using cached models.") || !strings.Contains(got, "no models are available for that provider") {
					t.Fatalf("chat = %q", got)
				}
			case "cancel":
				if got := plainRender(m.chatContainer); !strings.Contains(got, "Refreshing model catalog…") || strings.Contains(got, "Error:") || strings.Contains(got, "Warning:") {
					t.Fatalf("cancelled completion mutated UI: %q", got)
				}
				if m.opts.Model != nil || handle.persisted {
					t.Fatal("selected after shutdown")
				}
			default:
				if m.opts.Model != selected || handle.persisted {
					t.Fatal("refresh replaced model/session or selected after cancellation")
				}
			}
		})
	}
}

type postLoginOAuthProvider struct{ initiationCaptureOAuthProvider }

func (*postLoginOAuthProvider) ID() string   { return "opencode-go" }
func (*postLoginOAuthProvider) Name() string { return "OpenCode Go" }
func (*postLoginOAuthProvider) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{Access: "fake-access", Refresh: "fake-refresh"}, nil
}

func TestRegisteredOAuthLoginCompletesDefaultSelection(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.layout = tui.NewContainer()
	if err := m.runLoginRegisteredOAuth(t.Context(), &postLoginOAuthProvider{}, ""); err != nil {
		t.Fatal(err)
	}
	waitPostLoginStatus(t, m, "Selected kimi-k3")
	if got := modelSpec(m.opts.Model); got != "opencode-go/kimi-k3" {
		t.Fatalf("OAuth left model %q", got)
	}
	if got := plainRender(m.chatContainer); !strings.Contains(got, "Logged in to OpenCode Go. Selected kimi-k3. Credentials saved to ") {
		t.Fatalf("chat = %q", got)
	}
}

func TestPostLoginRefreshWarnings(t *testing.T) {
	for _, tc := range []struct {
		result CatalogRefreshResult
		want   string
	}{
		{CatalogRefreshResult{}, ""},
		{CatalogRefreshResult{Aborted: true}, "Logged in to Provider, but its model catalog refresh timed out; using cached models."},
		{CatalogRefreshResult{Errors: map[string]error{"provider": errors.New("bad gateway")}}, "Logged in to Provider, but its model catalog could not be refreshed; using cached models."},
	} {
		if got := catalogRefreshWarning("Logged in to Provider", tc.result); got != tc.want {
			t.Fatalf("warning = %q, want %q", got, tc.want)
		}
	}
}

func BenchmarkPostLoginModel(b *testing.B) {
	models := make([]tui.ModelSelectorItem, 4096)
	for i := range models {
		models[i] = tui.ModelSelectorItem{Provider: "other", ID: "model"}
	}
	models[len(models)-1] = tui.ModelSelectorItem{Provider: "opencode-go", ID: "kimi-k2.6"}
	b.ReportAllocs()
	for b.Loop() {
		postLoginModel("opencode-go", "Saved API key for OpenCode Go", "kimi-k2.6", models)
	}
}
