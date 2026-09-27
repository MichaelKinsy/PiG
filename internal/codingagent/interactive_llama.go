package codingagent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	"github.com/MichaelKinsy/PiG/tui"
)

// llamaUIBridge delivers the /llama flow's notifications to the interactive
// loop. While the manager view is showing, the loop is inside
// runEditorSlotCustom, so notifications queue there; otherwise the caller is
// already on the loop and runs them directly.
type llamaUIBridge struct {
	mu    sync.Mutex
	tasks chan func()
}

func (b *llamaUIBridge) setTasks(tasks chan func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tasks = tasks
}

func (b *llamaUIBridge) run(task func()) {
	b.mu.Lock()
	tasks := b.tasks
	b.mu.Unlock()
	if tasks == nil {
		task()
		return
	}
	tasks <- task
}

// runLlamaCommand runs Pi's built-in /llama command with the extension
// command context interactive mode provides.
func (m *InteractiveMode) runLlamaCommand(ctx context.Context) error {
	bridge := &llamaUIBridge{}
	return m.opts.Llama.HandleCommand(llama.CommandContext{
		Ctx:  ctx,
		Mode: "tui",
		Notify: func(message, notifyType string) {
			bridge.run(func() { m.showExtensionNotify(message, notifyType) })
		},
		Custom: func(factory func(requestRender, done func()) llama.CustomComponent) {
			m.runLlamaCustom(bridge, factory)
		},
	})
}

// showExtensionNotify mirrors interactive-mode.ts showExtensionNotify.
func (m *InteractiveMode) showExtensionNotify(message, notifyType string) {
	switch notifyType {
	case "error":
		m.showError(message)
	case "warning":
		m.showWarning(message)
	default:
		m.showStatus(message)
	}
}

// runLlamaCustom mirrors showExtensionCustom for the /llama view: the
// component replaces the editor until its flow calls done.
func (m *InteractiveMode) runLlamaCustom(bridge *llamaUIBridge, factory func(requestRender, done func()) llama.CustomComponent) {
	renderNotify := make(chan struct{}, 1)
	doneCh := make(chan struct{})
	tasks := make(chan func(), 16)
	bridge.setTasks(tasks)
	defer bridge.setTasks(nil)
	requestRender := func() {
		select {
		case renderNotify <- struct{}{}:
		default:
		}
	}
	var once sync.Once
	component := factory(requestRender, func() { once.Do(func() { close(doneCh) }) })
	m.runEditorSlotCustom(component, renderNotify, tasks, doneCh)
}

// runEditorSlotCustom shows component in the editor slot, feeding it input,
// repainting on request, and running queued loop tasks until done closes.
func (m *InteractiveMode) runEditorSlotCustom(component llama.CustomComponent, renderNotify <-chan struct{}, tasks <-chan func(), done <-chan struct{}) {
	m.editorContainer.SetChildren(component)
	m.tuiInst.Render()
	defer func() {
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()
	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()
	for {
		select {
		case buf := <-inputCh:
			for _, chunk := range dropKeyReleases(component, []string{string(buf)}) {
				component.HandleInput(chunk)
			}
		case <-renderNotify:
		case task := <-tasks:
			task()
		case <-done:
			for {
				select {
				case task := <-tasks:
					task()
				default:
					return
				}
			}
		}
		m.tuiInst.Render()
	}
}

// llamaAuthCollator orders the login list the way upstream sorts provider
// options by name with localeCompare.
var llamaAuthCollator = collate.New(language.Und)

// withLlamaLoginProvider adds the registered llama.cpp provider to the
// api-key login list at its name-ordered position.
func (m *InteractiveMode) withLlamaLoginProvider(providers []tui.OAuthProvider) []tui.OAuthProvider {
	if m.opts.Llama == nil {
		return providers
	}
	provider := m.opts.Llama.Provider()
	index := slices.IndexFunc(providers, func(candidate tui.OAuthProvider) bool {
		return llamaAuthCollator.CompareString(candidate.Name, provider.Name) > 0
	})
	if index < 0 {
		index = len(providers)
	}
	return slices.Insert(providers, index, tui.OAuthProvider{ID: provider.ID, Name: provider.Name, AuthType: "api_key"})
}

// applyLlamaAuthStatus reports llama.cpp configured through LLAMA_BASE_URL,
// which its auth check accepts without a stored credential.
func (m *InteractiveMode) applyLlamaAuthStatus(providers []tui.OAuthProvider) {
	if m.opts.Llama == nil {
		return
	}
	for index := range providers {
		if providers[index].ID != llama.LlamaProviderID || providers[index].Stored {
			continue
		}
		if check, err := m.opts.Llama.CheckAuth(context.Background()); err == nil && check != nil {
			providers[index].AuthStatusSource = string(ai.AuthSourceEnvironment)
			providers[index].AuthStatusLabel = check.Source
		}
	}
}

// loginAPIKeyProvider runs llama.cpp's own api-key login in the login dialog
// and reports false for providers that use the plain key prompt.
func (m *InteractiveMode) loginAPIKeyProvider(providerID string) bool {
	if m.opts.Llama == nil || providerID != llama.LlamaProviderID {
		return false
	}
	previousModel := m.opts.Model
	name := m.opts.Llama.Provider().Name
	parent := m.runCtx
	if parent == nil {
		parent = context.Background()
	}
	loginCtx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	dialog := m.newLoginDialog(name, func() { cancel(errLoginAborted) })
	renderNotify := make(chan struct{}, 1)
	requestRender := func() {
		select {
		case renderNotify <- struct{}{}:
		default:
		}
	}
	done := make(chan struct{})
	var loginErr error
	go func() {
		defer close(done)
		loginErr = m.opts.Llama.Login(llama.AuthInteraction{
			Ctx: loginCtx,
			Prompt: func(prompt llama.AuthPrompt) (string, error) {
				showInput := dialog.ShowInput
				// pig divergence (D80): preserve secret prompt metadata while allowing the configured Pi-compatible opt-out.
				if prompt.Type == "secret" {
					showInput = dialog.ShowSecretInput
				}
				answer := showInput(prompt.Message, prompt.Placeholder)
				requestRender()
				select {
				case value, ok := <-answer:
					if !ok {
						return "", errLoginCancelled
					}
					return value, nil
				case <-loginCtx.Done():
					return "", errLoginCancelled
				}
			},
		})
	}()
	m.runEditorSlotCustom(dialog, renderNotify, nil, done)
	if loginErr != nil {
		if loginErr.Error() != errLoginCancelled.Error() {
			m.showError(dialog.Redact(fmt.Sprintf("Failed to save API key for %s: %v", name, loginErr)))
		}
		return true
	}
	m.completeProviderAuthentication(providerID, name, ai.CredentialAPIKey, previousModel, filepath.Join(m.opts.AgentDir, "auth.json"), dialog.Redact)
	return true
}

var (
	errLoginCancelled = errors.New("Login cancelled")
	// errLoginAborted mirrors the AbortError a cancelled login signal gives
	// the server check that follows the prompts.
	errLoginAborted = errors.New("This operation was aborted")
)

// llamaCppPostLoginGuidance mirrors interactive-mode.ts llamaCppPostLoginGuidance.
func llamaCppPostLoginGuidance(actionLabel string, loadedModelCount int) string {
	if loadedModelCount == 0 {
		return actionLabel + ". No llama.cpp models are loaded. Use /llama to load a model, then /model to select it."
	}
	return actionLabel + ". Use /model to select a loaded llama.cpp model, or /llama to manage models."
}
