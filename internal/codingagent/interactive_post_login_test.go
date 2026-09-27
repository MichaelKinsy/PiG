package codingagent

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func newPostLoginTestMode(t *testing.T) *InteractiveMode {
	t.Helper()
	dir := t.TempDir()
	registry := NewModelRegistry(dir)
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry.SetAuthStorage(auth)
	m := NewInteractiveMode(InteractiveOptions{
		AgentDir: dir, ModelRegistry: registry, SettingsManager: NewSettingsManager(t.TempDir(), dir),
		ModelBuilder: func(spec string) (*ai.Model, error) {
			provider, id, _ := strings.Cut(spec, "/")
			return &ai.Model{ID: id, ProviderMeta: ai.ProviderMetadata{ProviderID: provider}}, nil
		},
	})
	m.chatContainer = tui.NewContainer()
	m.editorContainer = tui.NewContainer()
	m.editor = tui.NewEditor()
	m.tuiInst = tui.NewWithOutput(io.Discard, 200, 40)
	m.statusLine = NewStatusLine(nil, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{})
	m.backgroundCtx, m.backgroundCancel = context.WithCancel(t.Context())
	t.Cleanup(func() { m.backgroundCancel(); m.backgroundTasks.Wait() })
	return m
}

// Pi interactive-mode.ts:5879-5969 completes both authentication methods through
// one path, selecting and persisting the provider default only from unknown.
func TestAPIKeyLoginSelectsProviderDefault(t *testing.T) {
	for _, tc := range []struct{ provider, name, model string }{
		{"openai", "OpenAI", "gpt-5.5"},
		{"anthropic", "Anthropic", "claude-opus-4-8"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			if err := m.buildSlashContext(t.Context()).SetAPIKey(tc.provider, "test-secret"); err != nil {
				t.Fatal(err)
			}
			waitPostLoginStatus(t, m, "Selected "+tc.model)
			if got := modelSpec(m.opts.Model); got != tc.provider+"/"+tc.model {
				t.Fatalf("selected %q, want %s/%s", got, tc.provider, tc.model)
			}
			sm := NewSettingsManager(t.TempDir(), m.opts.AgentDir)
			if sm.GetDefaultProvider() != tc.provider || sm.GetDefaultModel() != tc.model {
				t.Fatalf("default not persisted: %+v", sm.Get())
			}
			want := "Saved API key for " + tc.name + ". Selected " + tc.model + ". Credentials saved to " + filepath.Join(m.opts.AgentDir, "auth.json")
			if got := plainRender(m.chatContainer); !strings.Contains(got, want) {
				t.Fatalf("chat = %s; want %s", got, want)
			}
		})
	}
}

func waitPostLoginStatus(t *testing.T, m *InteractiveMode, want string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !strings.Contains(plainRender(m.chatContainer), want) {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-deadline.C:
			t.Fatalf("missing %q: %s", want, plainRender(m.chatContainer))
		}
	}
}

func TestRegisteredOAuthLoginSelectsProviderDefault(t *testing.T) {
	for _, tc := range []struct{ provider, name, model string }{
		{"anthropic", "Anthropic", "claude-opus-4-8"},
		{"kimi-coding", "Kimi For Coding", "kimi-for-coding"},
		{"custom-oauth", "Custom OAuth", ""},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
			provider := successfulLoginProvider{parityOAuthProvider{id: tc.provider, name: tc.name}}
			if err := m.runLoginRegisteredOAuth(t.Context(), provider, ""); err != nil {
				t.Fatal(err)
			}
			if tc.model != "" {
				waitPostLoginStatus(t, m, "Logged in to "+tc.name+". Selected "+tc.model+". Credentials saved to")
				if got := modelSpec(m.opts.Model); got != tc.provider+"/"+tc.model {
					t.Fatalf("model = %s", got)
				}
				if got := m.opts.SettingsManager.GetDefaultModel(); got != tc.model {
					t.Fatalf("default = %s", got)
				}
			} else {
				waitPostLoginStatus(t, m, `Logged in to Custom OAuth, but no default model is configured for provider "custom-oauth". Use /model to select a model.`)
			}
		})
	}
}

type successfulLoginProvider struct{ parityOAuthProvider }

func (successfulLoginProvider) Login(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	return ai.OAuthCredentials{Access: "test-access", Refresh: "test-refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}, nil
}

func TestAPIKeyLoginWithoutDefaultReportsGuidance(t *testing.T) {
	m := newPostLoginTestMode(t)
	if err := m.buildSlashContext(t.Context()).SetAPIKey("custom-provider", "test-secret"); err != nil {
		t.Fatal(err)
	}
	want := `Saved API key for custom-provider, but no default model is configured for provider "custom-provider". Use /model to select a model.`
	if got := plainRender(m.chatContainer); !strings.Contains(got, want) {
		t.Fatalf("chat = %s; want %s", got, want)
	}
	if m.opts.Model != nil {
		t.Fatal("selected a model without a provider default")
	}
}
