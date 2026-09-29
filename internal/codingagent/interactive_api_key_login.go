package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type authPromptReply struct {
	value string
	err   error
}
type authPromptRequest struct {
	ctx    context.Context
	prompt ai.AuthPrompt
	reply  chan authPromptReply
}

// runAPIKeyLogin keeps the editor replaced until every provider prompt and credential write has settled. Only the modal loop handles terminal input; provider callbacks and persistence run on its joined worker.
func (m *InteractiveMode) runAPIKeyLogin(provider tui.OAuthProvider) error {
	method := m.providerAuth(provider.ID).APIKey
	if method == nil {
		return fmt.Errorf("%s does not support api_key login", provider.Name)
	}
	previousModel := m.opts.Model
	registry := m.opts.ModelRegistry
	ctx, cancel := context.WithCancelCause(m.loginContext())
	defer cancel(nil)
	dialog := m.newLoginDialog(provider.Name, func() { cancel(errLoginAborted) })
	requests := make(chan authPromptRequest)
	notifications := make(chan ai.AuthEvent)
	done := make(chan error, 1)
	authPath := filepath.Join(m.opts.AgentDir, "auth.json")
	if method.Login == nil {
		dialog = m.newLoginDialog(provider.Name, func() { cancel(errLoginAborted) }, provider.Name+" setup")
		dialog.ShowInfo(method.Name+" is configured outside pig.", nil, true)
		go func() { <-ctx.Done(); done <- nil }()
		return m.runAuthDialog(ctx, cancel, dialog, nil, nil, done)
	}
	// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:showApiKeyLoginDialog
	if provider.ID == "amazon-bedrock" {
		theme := tui.ActiveTheme()
		dialog.ShowDetails([]string{
			theme.FgText("text", "You can also use an AWS profile, IAM keys, or role-based credentials."),
			theme.FgText("muted", "See:"),
			theme.FgText("accent", "  "+filepath.Join(ConfigRoot(), "docs", "providers.md")),
		})
	}
	go func() {
		interaction := ai.AuthInteraction{
			Prompt: func(promptCtx context.Context, prompt ai.AuthPrompt) (string, error) {
				request := authPromptRequest{ctx: promptCtx, prompt: prompt, reply: make(chan authPromptReply, 1)}
				select {
				case requests <- request:
				case <-promptCtx.Done():
					return "", context.Cause(promptCtx)
				case <-ctx.Done():
					return "", context.Cause(ctx)
				}
				select {
				case reply := <-request.reply:
					return reply.value, reply.err
				case <-promptCtx.Done():
					return "", context.Cause(promptCtx)
				case <-ctx.Done():
					return "", context.Cause(ctx)
				}
			}, Notify: func(event ai.AuthEvent) {
				select {
				case notifications <- event:
				case <-ctx.Done():
				}
			},
		}
		credential, err := method.Login(ctx, interaction)
		if ctx.Err() != nil {
			err = context.Cause(ctx)
		}
		if err == nil {
			auth, openErr := ai.NewAuthStorage(authPath)
			err = openErr
			if err == nil {
				var credentials ai.CredentialStore = auth
				if registry != nil {
					credentials = registryCredentials{registry}
				}
				_, err = credentials.Modify(ctx, provider.ID, func(*ai.Credential) (*ai.Credential, error) { return &credential, nil })
			}
			if err == nil && registry != nil {
				registry.Refresh()
			}
		}
		if err != nil && ctx.Err() != nil {
			err = context.Cause(ctx)
		}
		done <- err
	}()
	loginErr := m.runAuthDialog(ctx, cancel, dialog, requests, notifications, done)
	if loginErr != nil {
		if loginErr.Error() != errLoginCancelled.Error() {
			m.showError(dialog.Redact(fmt.Sprintf("Failed to save API key for %s: %v", provider.Name, loginErr)))
		}
		return nil
	}
	m.completeProviderAuthentication(provider.ID, provider.Name, ai.CredentialAPIKey, previousModel, authPath, dialog.Redact)
	return nil
}

func (m *InteractiveMode) runAuthDialog(ctx context.Context, cancel context.CancelCauseFunc, dialog *tui.LoginDialog, requests <-chan authPromptRequest, notifications <-chan ai.AuthEvent, done <-chan error) error {
	m.editorContainer.SetChildren(dialog)
	m.tuiInst.Render()
	defer func() { m.editorContainer.SetChildren(m.editor); m.tuiInst.RequestRender() }()
	inputCh, release := m.acquireModalInputChannel()
	defer release()
	var request *authPromptRequest
	var answer <-chan string
	var selector *tui.ExtensionSelectorComponent
	var promptDone <-chan struct{}
	var ownerDone = ctx.Done()
	inputErrors := m.inputErrCh
	for {
		if m.modalStopped() {
			cancel(errLoginAborted)
			inputCh = nil
			requests = nil
			notifications = nil
		}
		select {
		case err := <-inputErrors:
			m.inputLoopErr = err
			inputErrors = nil
			cancel(errLoginAborted)
		case next := <-requests:
			request = &next
			promptDone = next.ctx.Done()
			switch prompt := next.prompt.(type) {
			case ai.AuthSelectPrompt:
				labels := make([]string, len(prompt.Options))
				for i, option := range prompt.Options {
					labels[i] = option.Label
				}
				selector = tui.NewExtensionSelector(prompt.Message, labels)
				m.editorContainer.SetChildren(selector)
			case ai.AuthSecretPrompt:
				answer = dialog.ShowSecretInput(prompt.Message, prompt.Placeholder)
			case ai.AuthTextPrompt:
				answer = dialog.ShowInput(prompt.Message, prompt.Placeholder)
			case ai.AuthManualCodePrompt:
				answer = dialog.ShowManualInput(prompt.Message)
			}
		case event := <-notifications:
			switch e := event.(type) {
			case ai.AuthInfoEvent:
				links := make([]tui.AuthInfoLink, len(e.Links))
				for i, link := range e.Links {
					links[i] = tui.AuthInfoLink{Label: link.Label, URL: link.URL}
				}
				dialog.ShowInfo(e.Message, links, false)
			case ai.AuthProgressEvent:
				dialog.ShowProgress(e.Message)
			case ai.AuthURLEvent:
				dialog.ShowAuth(e.URL, e.Instructions)
				dialog.ShowWaiting("Waiting for authentication...")
				if !parityHarnessEnabled() {
					_ = openBrowser(e.URL)
				}
			case ai.AuthDeviceCodeEvent:
				showDeviceCode(dialog, e.VerificationURI, e.UserCode)
			}
		case value, ok := <-answer:
			reply := authPromptReply{value: value}
			if !ok {
				reply.err = errLoginAborted
			}
			request.reply <- reply
			request = nil
			answer = nil
			promptDone = nil
		case <-promptDone:
			request = nil
			answer = nil
			selector = nil
			promptDone = nil
			m.editorContainer.SetChildren(dialog)
		case buf, ok := <-inputCh:
			if !ok {
				cancel(errLoginAborted)
				inputCh = nil
				break
			}
			var component tui.Component = dialog
			if selector != nil {
				component = selector
			}
			for _, chunk := range m.modalInputChunks(component, []string{string(buf)}) {
				if selector != nil {
					selector.HandleInput(chunk)
					if selector.Done() {
						reply := authPromptReply{}
						if selector.Cancelled() {
							reply.err = errLoginCancelled
						} else {
							prompt := request.prompt.(ai.AuthSelectPrompt)
							reply.value = prompt.Options[selector.SelectedIndex()].ID
						}
						request.reply <- reply
						request = nil
						selector = nil
						promptDone = nil
						m.editorContainer.SetChildren(dialog)
					}
				} else {
					dialog.HandleInput(chunk)
				}
			}
		case task := <-m.uiTaskCh:
			task()
		case <-m.renderWakeCh:
			m.runScheduledRender()
		case <-ownerDone:
			// The worker owns the final error. Stop admitting input and prompts, but service owner tasks until its work settles.
			ownerDone = nil
			inputCh = nil
			requests = nil
			notifications = nil
			answer = nil
			request = nil
			selector = nil
			promptDone = nil
		case err := <-done:
			return err
		}
		m.tuiInst.Render()
	}
}
