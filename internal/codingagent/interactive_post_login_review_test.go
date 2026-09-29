package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

type externalPathLoginProvider struct {
	successfulLoginProvider
	path string
}

func (p externalPathLoginProvider) OAuthCredentialStatus() (ai.OAuthCredentialStatus, bool) {
	return ai.OAuthCredentialStatus{}, false
}
func (p externalPathLoginProvider) DeleteOAuthCredentials() (bool, error) { return false, nil }
func (p externalPathLoginProvider) StoreOAuthCredentials(ai.OAuthCredentials) (string, error) {
	return p.path, os.WriteFile(p.path, []byte("fixture credentials"), 0o600)
}

func TestAPIKeyLoginPromptMasksInput(t *testing.T) {
	for _, provider := range []string{"openai", "google"} {
		t.Run(provider, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
			sc := m.buildSlashContext(t.Context())
			sc.Args = provider
			sc.SelectAuthMethod = func([]tui.OAuthProvider) (string, bool) { return "api_key", true }
			done := make(chan error, 1)
			go func() { done <- loginHandler(sc) }()
			auth, err := ai.BuiltinProviderAuth(provider)
			if err != nil {
				t.Fatal(err)
			}
			waitForRender(t, m.editorContainer, "Enter "+auth.APIKey.Name)
			const secret = "api-key-not-for-terminal"
			deliverModalInput(t, m, []byte(secret))
			// The next delivery waits until the first input handler and render finish.
			deliverModalInput(t, m, []byte(""))
			leaked := strings.Contains(plainRender(m.editorContainer), secret)
			deliverModalInput(t, m, []byte("\r"))
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			store, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			received, ok, err := store.Get(provider)
			if err != nil || !ok || received.Key != secret {
				t.Fatal("API key prompt changed the provider's value")
			}
			if leaked {
				t.Fatal("ordinary API-key login rendered its secret")
			}
		})
	}
}

// External stores are PiG's contributed-auth capability (D40), not Pi's auth.json store.
func TestRegisteredOAuthLoginReportsReturnedCredentialPath(t *testing.T) {
	for _, providerID := range []string{"custom-store", "anthropic"} {
		t.Run(providerID, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
			path := filepath.Join(t.TempDir(), "external-credentials.json")
			provider := externalPathLoginProvider{successfulLoginProvider: successfulLoginProvider{parityOAuthProvider{id: providerID, name: "External Store"}}, path: path}
			if providerID == "anthropic" {
				m.opts.ModelRegistry.SetRuntimeAPIKey(providerID, "test-key")
			}
			if err := m.runLoginRegisteredOAuth(t.Context(), provider, ""); err != nil {
				t.Fatal(err)
			}
			waitPostLoginStatus(t, m, "Credentials saved to")
			got := plainRender(m.chatContainer)
			if !strings.Contains(got, "Credentials saved to "+path) || strings.Contains(got, filepath.Join(m.opts.AgentDir, "auth.json")) {
				t.Fatalf("wrong credential location: %s", got)
			}
		})
	}
}

// Model construction can wait for credentials. A /model or /new that runs while
// it waits must invalidate the pending login before it mutates any Session state.
func TestPostLoginSelectionLosesToOwnerLoopCommands(t *testing.T) {
	for _, command := range []string{"model", "same model", "new"} {
		t.Run(command, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := newPostLoginTestMode(t)
				m.opts.CWD = t.TempDir()
				m.opts.SessionHandle = &beforeUIHandle{
					recordingCompactHandle: &recordingCompactHandle{agent: m.agent},
					runner:                 inproc.NewRunner(nil, m.opts.CWD),
					sm:                     NewSessionManagerWithDir(m.opts.CWD, t.TempDir()),
				}
				unknown := &ai.Model{ID: "unknown", ProviderMeta: ai.ProviderMetadata{ProviderID: "unknown", API: "unknown"}}
				if command == "same model" {
					m.opts.Model = unknown
					m.agent.SetModel(unknown)
				}
				started, release := make(chan struct{}), make(chan struct{})
				build := m.opts.ModelBuilder
				m.opts.ModelBuilder = func(spec string) (*ai.Model, error) {
					if spec == "openai/gpt-5.5" {
						close(started)
						<-release
					}
					if spec == "unknown/unknown" {
						return unknown, nil
					}
					return build(spec)
				}
				if err := setPostLoginAPIKey(m, "openai", "test-key"); err != nil {
					t.Fatal(err)
				}
				<-started
				if command == "model" || command == "same model" {
					spec := "fixture/chosen"
					if command == "same model" {
						spec = "unknown/unknown"
					}
					if err := m.buildSlashContext(t.Context()).SwitchModel(spec); err != nil {
						t.Fatal(err)
					}
				} else if err := m.buildSlashContext(t.Context()).NewSession(); err != nil {
					t.Fatal(err)
				}
				expected := m.opts.Model
				expectedAgent := m.agent.Model()
				close(release)
				drainPostLoginTasks(m)
				if m.opts.Model != expected || m.agent.Model() != expectedAgent {
					t.Fatalf("late login changed model: UI=%s agent=%v", modelSpec(m.opts.Model), m.agent.Model())
				}
				if m.opts.SettingsManager.GetDefaultModel() != "" {
					t.Fatal("stale login persisted the default")
				}
				if strings.Contains(plainRender(m.chatContainer), "Selected gpt-5.5") {
					t.Fatal("stale login announced selection")
				}
			})
		})
	}
}

type blockedPostLoginHandle struct {
	recordingCompactHandle
	started, release chan struct{}
	afterMutation    bool
	persisted        []string
}

func (h *blockedPostLoginHandle) SetModel(model *ai.Model, options ...ModelMutationOptions) error {
	return h.setModel(model, options, func(mutate func() error) error { return mutate() })
}
func (h *blockedPostLoginHandle) SetModelOnMain(model *ai.Model, options ModelMutationOptions, dispatch func(func() error) error) error {
	return h.setModel(model, []ModelMutationOptions{options}, dispatch)
}
func (h *blockedPostLoginHandle) setModel(model *ai.Model, options []ModelMutationOptions, dispatch func(func() error) error) error {
	login := model.ID == "gpt-5.5"
	if login && !h.afterMutation {
		close(h.started)
		<-h.release
	}
	if err := dispatch(func() error {
		h.agent.SetModel(model)
		if len(options) > 0 && options[0].Persist {
			h.persisted = append(h.persisted, model.ID)
		}
		return nil
	}); err != nil {
		return err
	}
	if login && h.afterMutation {
		close(h.started)
		<-h.release
	}
	return nil
}

// Cover the second await boundary too: both a delayed state commit and delayed
// extension notification must not overwrite a newer owner-loop choice.
func TestPostLoginSelectionRechecksSessionMutationAndCompletion(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before state mutation", true: "after state mutation"}[after], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := newPostLoginTestMode(t)
				h := &blockedPostLoginHandle{recordingCompactHandle: recordingCompactHandle{agent: m.agent, inner: NewSession("login-race", m.opts.AgentDir)}, started: make(chan struct{}), release: make(chan struct{}), afterMutation: after}
				m.opts.SessionHandle = h
				if err := setPostLoginAPIKey(m, "openai", "test-key"); err != nil {
					t.Fatal(err)
				}
				// Serve the owner-loop queue while SetModel reaches the controlled await.
				waiting := true
				for waiting {
					select {
					case <-h.started:
						waiting = false
					case task := <-m.uiTaskCh:
						task()
					}
				}
				sc := m.buildSlashContext(context.Background())
				if err := sc.SwitchModel("fixture/chosen"); err != nil {
					t.Fatal(err)
				}
				if err := m.persistDefaultModel(m.opts.Model); err != nil {
					t.Fatal(err)
				}
				expected := m.opts.Model
				close(h.release)
				drainPostLoginTasks(m)
				if m.opts.Model != expected || m.agent.Model() != expected {
					t.Fatal("late SetModel completion replaced the owner-loop model")
				}
				if got := m.opts.SettingsManager.GetDefaultModel(); got != "chosen" {
					t.Fatalf("late completion persisted %q", got)
				}
				if !after && len(h.persisted) != 0 {
					t.Fatalf("stale state commit persisted %v", h.persisted)
				}
			})
		})
	}
}
