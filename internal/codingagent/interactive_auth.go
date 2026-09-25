package codingagent

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// authSelectorProviderNames holds the provider names (ai/src/providers/*.ts
// `name`) that upstream's auth selector shows where they differ from the OAuth
// flow's own name (for example meta.ts: "Meta", not "Meta (Muse subscription)").
var authSelectorProviderNames = map[string]string{
	"meta":         "Meta",
	"openai-codex": "OpenAI Codex",
}

// oauthProviderList returns the auth providers available to /login and /logout.
func (m *InteractiveMode) oauthProviderList(mode string) []tui.OAuthProvider {
	var all []tui.OAuthProvider
	for _, provider := range m.oauthProviders() {
		name := provider.Name()
		if providerName, ok := authSelectorProviderNames[provider.ID()]; ok {
			name = providerName
		}
		all = append(all, tui.OAuthProvider{ID: provider.ID(), Name: name, AuthType: "oauth"})
	}
	slices.SortFunc(all, func(a, b tui.OAuthProvider) int {
		return strings.Compare(a.Name, b.Name)
	})
	if mode == "login-api-key" {
		// Pull the canonical API-key provider list from ai/ so any drift between
		// the selector and the rest of the codebase is impossible. github-copilot
		// is intentionally excluded: it belongs to the OAuth/subscription list.
		all = nil
		for _, p := range ai.APIKeyProviders() {
			all = append(all, tui.OAuthProvider{ID: p.ID, Name: p.Name, AuthType: "api_key"})
		}
		all = m.withLlamaLoginProvider(all)
	}
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return all
	}
	creds, err := auth.Load()
	if err != nil {
		return all
	}
	for i := range all {
		if c, ok := creds[all[i].ID]; ok {
			all[i].Stored = true
			all[i].StoredType = string(c.Type)
			all[i].AuthStatusSource = "stored"
		}
		if !all[i].Stored {
			if store, ok := oauthCredentialStore(all[i].ID); ok {
				if status, ok := store.OAuthCredentialStatus(); ok {
					all[i].Stored = true
					all[i].StoredType = status.AuthType
					all[i].AuthStatusSource = status.Source
				}
			}
		}
		if all[i].AuthType == "api_key" {
			status := auth.GetAuthStatus(all[i].ID)
			if !all[i].Stored {
				all[i].AuthStatusSource = string(status.Source)
				all[i].AuthStatusLabel = status.Label
			}
		}
	}
	m.applyLlamaAuthStatus(all)
	if mode == "logout" {
		logged := make([]tui.OAuthProvider, 0, len(all))
		for _, p := range all {
			if p.Stored {
				logged = append(logged, p)
			}
		}
		return logged
	}
	return all
}

func oauthCredentialStore(providerID string) (ai.OAuthCredentialStore, bool) {
	provider, ok := ai.GetOAuthProvider(providerID)
	if !ok {
		return nil, false
	}
	store, ok := provider.(ai.OAuthCredentialStore)
	return store, ok
}

// beginLogin registers cancel as the active background-login canceller and
// returns a generation token. A later endLogin(token) clears it only if no
// newer login has replaced it.
func (m *InteractiveMode) beginLogin(cancel context.CancelFunc) int {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	m.activeLoginGen++
	m.activeLoginCancel = cancel
	return m.activeLoginGen
}

// endLogin clears the active-login canceller if it still belongs to token,
// called when a login goroutine finishes so a subsequent Esc/Ctrl+C is not
// swallowed by a stale login.
func (m *InteractiveMode) endLogin(token int) {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	if m.activeLoginGen == token {
		m.activeLoginCancel = nil
	}
}

// cancelActiveLogin aborts an in-progress background login if one is active,
// returning true when it consumed the request. Safe to call on every
// interrupt/clear keystroke: it is a no-op when no login is running.
func (m *InteractiveMode) cancelActiveLogin() bool {
	m.loginMu.Lock()
	cancel := m.activeLoginCancel
	m.activeLoginCancel = nil
	m.loginMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// runOAuthLogin runs the interactive OAuth login flow for a provider.
// Mirrors upstream showLoginDialog (interactive-mode.ts:4325-4444).
// Uses status-line flash + chat messages for the device-flow state.
func (m *InteractiveMode) runOAuthLogin(loginCtx context.Context, provider string) error {
	switch provider {
	case "github-copilot":
		return m.runLoginGitHubCopilot(loginCtx)
	case "anthropic":
		return m.runLoginAnthropic(loginCtx)
	case "openai-codex":
		return m.runLoginOpenAICodex(loginCtx)
	default:
		oauthProvider, ok := m.lookupOAuthProvider(provider)
		if !ok {
			return fmt.Errorf("unknown OAuth provider %q", provider)
		}
		method, selected := m.selectOAuthLoginMethod(oauthProvider)
		if !selected {
			return nil
		}
		return m.runLoginRegisteredOAuth(loginCtx, oauthProvider, method)
	}
}

// lookupOAuthProvider resolves a /login provider ID to its OAuth flow. A
// configured Radius provider (built-in or a models.json gateway) owns its ID.
func (m *InteractiveMode) lookupOAuthProvider(providerID string) (ai.OAuthProviderInterface, bool) {
	if m.opts.ModelRegistry != nil {
		if flow, ok := m.opts.ModelRegistry.RadiusOAuth(providerID); ok {
			return flow, true
		}
	}
	return ai.GetOAuthProvider(providerID)
}

// oauthProviders lists the registered OAuth flows with the registry's Radius
// flows replacing or adding entries by provider ID.
func (m *InteractiveMode) oauthProviders() []ai.OAuthProviderInterface {
	providers := ai.GetOAuthProviders()
	if m.opts.ModelRegistry == nil {
		return providers
	}
	for _, flow := range m.opts.ModelRegistry.RadiusOAuthFlows() {
		providers = slices.DeleteFunc(providers, func(provider ai.OAuthProviderInterface) bool { return provider.ID() == flow.ID() })
		providers = append(providers, flow)
	}
	return providers
}

// refreshCatalogAfterLogin mirrors the upstream post-login catalog refresh:
// the provider's dynamic catalog refreshes in the background for up to 15 s,
// and a timeout or failure leaves the cached models in use with a warning.
func (m *InteractiveMode) refreshCatalogAfterLogin(providerID, actionLabel string) {
	registry := m.opts.ModelRegistry
	if registry == nil {
		return
	}
	if _, dynamic := registry.RadiusOAuth(providerID); !dynamic {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result := registry.RefreshCatalogs(ctx, CatalogRefreshOptions{AllowNetwork: ModelNetworkEnabled(), Providers: []string{providerID}})
		warning := catalogRefreshWarning(actionLabel, result)
		m.postUITask(func() {
			if warning != "" {
				m.showWarning(warning)
			}
			m.updateProviderInfo()
		})
	}()
}

func catalogRefreshWarning(actionLabel string, result CatalogRefreshResult) string {
	switch {
	case result.Aborted:
		return actionLabel + ", but its model catalog refresh timed out; using cached models."
	case len(result.Errors) > 0:
		return actionLabel + ", but its model catalog could not be refreshed; using cached models."
	}
	return ""
}

// oauthLoginMethodPrompter is implemented by flows that ask for a sign-in
// method before login (Radius). The selection runs before the login dialog
// opens, like the OpenAI Codex method selector.
type oauthLoginMethodPrompter interface {
	LoginMethodPrompt() ai.OAuthSelectPrompt
}

// oauthContextLogin is implemented by flows whose login honors cancellation.
type oauthContextLogin interface {
	LoginContext(ctx context.Context, callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error)
}

// selectOAuthLoginMethod returns "" and true when the provider asks nothing.
func (m *InteractiveMode) selectOAuthLoginMethod(provider ai.OAuthProviderInterface) (string, bool) {
	prompter, ok := provider.(oauthLoginMethodPrompter)
	if !ok {
		return "", true
	}
	prompt := prompter.LoginMethodPrompt()
	labels := make([]string, 0, len(prompt.Options))
	for _, option := range prompt.Options {
		labels = append(labels, option.Label)
	}
	index, ok := m.runEditorSlotExtensionSelector(tui.NewExtensionSelector(prompt.Message, labels))
	if !ok || index < 0 || index >= len(prompt.Options) {
		return "", false
	}
	return prompt.Options[index].ID, true
}

func runOAuthProviderLogin(ctx context.Context, provider ai.OAuthProviderInterface, callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	if contextual, ok := provider.(oauthContextLogin); ok {
		return contextual.LoginContext(ctx, callbacks)
	}
	return provider.Login(callbacks)
}

func (m *InteractiveMode) runLoginRegisteredOAuth(loginCtx context.Context, provider ai.OAuthProviderInterface, selectedMethod string) error {
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return fmt.Errorf("auth storage: %w", err)
	}

	loginCtx, loginCancel := context.WithCancel(loginCtx)
	dlg := tui.NewLoginDialog(provider.Name(), loginCancel)
	renderNotify := make(chan struct{}, 16)
	notify := func() {
		select {
		case renderNotify <- struct{}{}:
		default:
		}
	}

	prompt := func(ctx context.Context, value ai.OAuthPrompt) (string, error) {
		ch := dlg.ShowInput(value.Message, value.Placeholder)
		notify()
		extension.CallInitiated(ctx)
		select {
		case input, ok := <-ch:
			if !ok {
				return "", fmt.Errorf("Login cancelled")
			}
			if strings.TrimSpace(input) == "" && !value.AllowEmpty {
				return "", fmt.Errorf("%s is required", value.Message)
			}
			return input, nil
		case <-loginCtx.Done():
			return "", loginCtx.Err()
		}
	}
	manualCode := func(ctx context.Context) (string, error) {
		ch := dlg.ShowInput("Paste redirect URL below, or complete login in browser:", "")
		notify()
		extension.CallInitiated(ctx)
		select {
		case input, ok := <-ch:
			if !ok {
				return "", fmt.Errorf("Login cancelled")
			}
			return input, nil
		case <-loginCtx.Done():
			return "", loginCtx.Err()
		}
	}
	selectMethod := func(ctx context.Context, value ai.OAuthSelectPrompt) (string, error) {
		if selectedMethod != "" {
			extension.CallInitiated(ctx)
			return selectedMethod, nil
		}
		if len(value.Options) == 0 {
			extension.CallInitiated(ctx)
			return "", fmt.Errorf("%s has no options", value.Message)
		}
		extension.CallInitiated(ctx)
		return "", fmt.Errorf("interactive selection is not available for %s; use a provider-specific login command", provider.Name())
	}

	cb := ai.OAuthLoginCallbacks{
		OnPrompt:        func(value ai.OAuthPrompt) (string, error) { return prompt(context.Background(), value) },
		OnPromptContext: prompt,
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			dlg.ShowAuth(info.VerificationURI, fmt.Sprintf("Enter code: %s", info.UserCode))
			notify()
			if !parityHarnessEnabled() {
				_ = openBrowser(info.VerificationURI)
			}
		},
		OnAuth: func(info ai.OAuthAuthInfo) {
			dlg.ShowAuth(info.URL, info.Instructions)
			notify()
			if !parityHarnessEnabled() {
				_ = openBrowser(info.URL)
			}
		},
		OnManualCodeInput:        func() (string, error) { return manualCode(context.Background()) },
		OnManualCodeInputContext: manualCode,
		OnProgress: func(msg string) {
			dlg.ShowProgress(msg)
			notify()
		},
		OnSelect:        func(value ai.OAuthSelectPrompt) (string, error) { return selectMethod(context.Background(), value) },
		OnSelectContext: selectMethod,
	}

	var authPath string
	go func() {
		defer loginCancel()

		cred, err := runOAuthProviderLogin(loginCtx, provider, cb)
		if err != nil {
			if loginCtx.Err() == nil {
				dlg.ShowProgress(fmt.Sprintf("Login failed: %v", err))
				notify()
			}
			return
		}

		if store, ok := provider.(ai.OAuthCredentialStore); ok {
			authPath, err = store.StoreOAuthCredentials(cred)
		} else {
			err = auth.Set(provider.ID(), ai.Credential{Type: ai.CredentialOAuth, Refresh: cred.Refresh, Access: cred.Access, Expires: cred.Expires, ProjectID: cred.ProjectID, Scope: cred.Scope})
			authPath = auth.Path()
		}
		if err != nil {
			dlg.ShowProgress(fmt.Sprintf("Failed to store credentials: %v", err))
			notify()
			return
		}

		if m.opts.ModelRegistry != nil {
			m.opts.ModelRegistry.Refresh()
		}
		dlg.Success()
		notify()
	}()

	ok := m.runEditorSlotLoginDialog(dlg, renderNotify)
	if ok {
		m.showStatus(fmt.Sprintf("Logged in to %s. Credentials saved to %s", provider.Name(), authPath))
		m.updateProviderInfo()
		m.refreshCatalogAfterLogin(provider.ID(), "Logged in to "+provider.Name())
	}
	return nil
}

// runLoginOpenAICodex runs the OpenAI Codex (ChatGPT) OAuth flow.
// Mirrors upstream openai-codex.ts login(), which first presents a method
// selector (browser vs device-code) via onSelect, then runs the chosen flow.
// The browser path uses the PKCE + localhost callback dialog; the device-code
// path (RFC 8628) shows the user code while polling.
func (m *InteractiveMode) runLoginOpenAICodex(loginCtx context.Context) error {
	// Method selector, matching upstream openai-codex.ts login()'s onSelect call.
	methodSel := tui.NewExtensionSelector("Select OpenAI Codex login method:", []string{
		"Browser login (default)",
		"Device code login (headless)",
	})
	methodIdx, methodOK := m.runEditorSlotExtensionSelector(methodSel)
	if !methodOK {
		return nil
	}
	loginMethod := ai.OpenAICodexBrowserLoginMethod
	if methodIdx == 1 {
		loginMethod = ai.OpenAICodexDeviceCodeLoginMethod
	}

	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return fmt.Errorf("auth storage: %w", err)
	}

	loginCtx, loginCancel := context.WithCancel(loginCtx)
	dlg := tui.NewLoginDialog(buildAuthProviderName("openai-codex"), loginCancel)
	renderNotify := make(chan struct{}, 16)
	notify := func() {
		select {
		case renderNotify <- struct{}{}:
		default:
		}
	}

	cb := ai.OAuthLoginCallbacks{
		OnSelect: func(ai.OAuthSelectPrompt) (string, error) {
			return loginMethod, nil
		},
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			dlg.ShowAuth(info.VerificationURI, fmt.Sprintf("Enter code: %s", info.UserCode))
			notify()
			if !parityHarnessEnabled() {
				_ = openBrowser(info.VerificationURI)
			}
		},
		OnAuth: func(info ai.OAuthAuthInfo) {
			dlg.ShowAuth(info.URL, info.Instructions)
			notify()
			if !parityHarnessEnabled() {
				_ = openBrowser(info.URL)
			}
		},
		OnManualCodeInput: func() (string, error) {
			ch := dlg.ShowInput("Paste redirect URL below, or complete login in browser:", "")
			notify()
			select {
			case v, ok := <-ch:
				if !ok {
					return "", fmt.Errorf("Login cancelled")
				}
				return v, nil
			case <-loginCtx.Done():
				return "", loginCtx.Err()
			}
		},
		OnProgress: func(msg string) {
			dlg.ShowProgress(msg)
			notify()
		},
	}

	// authPath is written by the goroutine before dlg.Success() and read after
	// the dialog closes; the dlg.mu edge in Success -> Done() (observed by
	// runEditorSlotLoginDialog) publishes it to the post-dialog code below.
	var authPath string
	go func() {
		defer loginCancel()

		cred, err := ai.LoginOpenAICodex(loginCtx, cb)
		if err != nil {
			if loginCtx.Err() == nil {
				dlg.ShowProgress(fmt.Sprintf("Login failed: %v", err))
				notify()
			}
			return
		}

		codexCred := ai.Credential{
			Type:    ai.CredentialOAuth,
			Refresh: cred.Refresh,
			Access:  cred.Access,
			Expires: cred.Expires,
		}
		if err := auth.Set("openai-codex", codexCred); err != nil {
			dlg.ShowProgress(fmt.Sprintf("Failed to store credentials: %v", err))
			notify()
			return
		}

		if m.opts.ModelRegistry != nil {
			m.opts.ModelRegistry.Refresh()
		}
		authPath = auth.Path()
		dlg.Success()
		notify()
	}()

	ok := m.runEditorSlotLoginDialog(dlg, renderNotify)
	// Back on the main input-loop goroutine (dialog closed). Apply post-login UI
	// here rather than from the login goroutine: during the dialog the main
	// goroutine ran runEditorSlotLoginDialog's own loop, not the inputLoop
	// select, so it did not drain uiTaskCh and a runOnMain post would deadlock.
	// ok == !Cancelled() is true only when the goroutine reached dlg.Success().
	if ok {
		m.showStatus(fmt.Sprintf("Logged in to OpenAI Codex. Credentials saved to %s", authPath))
		m.updateProviderInfo()
	}
	return nil
}

// runLoginAnthropic runs the Anthropic Claude Pro/Max OAuth flow (PKCE +
// localhost callback). Mirrors the github-copilot login layout but uses
// the authorization-code/PKCE shape from ai.LoginAnthropic. The manual
// "paste redirect URL" fallback used by upstream's LoginDialogComponent
// is intentionally not wired here: the pig line renderer does not own
// the editor input the way the upstream overlay does, and the localhost
// callback covers the same-machine path. To use the manual-paste path on
// a remote machine, run `pig login anthropic` from a terminal that can
// reach the callback URL, or set ANTHROPIC_API_KEY directly.
func (m *InteractiveMode) runLoginAnthropic(loginCtx context.Context) error {
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return fmt.Errorf("auth storage: %w", err)
	}

	m.appendChatBlock(tui.NewMarkdown("**Login to " + ai.AnthropicOAuthDisplayName + "**"))

	loginCtx, loginCancel := context.WithCancel(loginCtx)
	loginToken := m.beginLogin(loginCancel)

	cb := ai.OAuthLoginCallbacks{
		OnAuth: func(info ai.OAuthAuthInfo) {
			msg := fmt.Sprintf(
				"1. Open: %s\n2. Authorize in your browser.\n3. The callback will return automatically.\n\n%s\n\nWaiting for authorization... (Ctrl+C to cancel)",
				info.URL, info.Instructions)
			m.runOnMain(m.runCtx, func() {
				m.appendChatBlock(tui.NewMarkdown(msg))
				m.tuiInst.Render()
			})
			_ = openBrowser(info.URL)
		},
		OnProgress: func(msg string) { m.runOnMain(m.runCtx, func() { m.showStatus(msg) }) },
	}

	go func() {
		defer loginCancel()
		defer m.endLogin(loginToken)

		cred, err := ai.LoginAnthropic(loginCtx, cb)
		if err != nil {
			m.runOnMain(m.runCtx, func() {
				if loginCtx.Err() != nil {
					m.appendChatBlock(tui.NewMarkdown("Login cancelled."))
				} else {
					m.appendChatBlock(tui.NewMarkdown(fmt.Sprintf("Login failed: %v", err)))
				}
				m.tuiInst.ForceFullRender()
				m.tuiInst.Render()
			})
			return
		}

		anthCred := ai.Credential{
			Type:    ai.CredentialOAuth,
			Refresh: cred.Refresh,
			Access:  cred.Access,
			Expires: cred.Expires,
		}
		if err := auth.Set("anthropic", anthCred); err != nil {
			m.runOnMain(m.runCtx, func() {
				m.appendChatBlock(tui.NewMarkdown(fmt.Sprintf("Failed to store credentials: %v", err)))
				m.tuiInst.ForceFullRender()
				m.tuiInst.Render()
			})
			return
		}

		if m.opts.ModelRegistry != nil {
			m.opts.ModelRegistry.Refresh()
		}

		// Model selection touches shared state (m.opts.Model, statusLine, the
		// agent/session model) that the main loop reads, so apply it there.
		m.runOnMain(m.runCtx, func() {
			authPath := auth.Path()
			status := fmt.Sprintf("Logged in to Anthropic. Credentials saved to %s", authPath)
			if m.opts.Model == nil && m.opts.ModelBuilder != nil {
				if newModel, err := m.opts.ModelBuilder("anthropic/claude-sonnet-4-5"); err == nil {
					if m.opts.SessionHandle != nil {
						_ = m.opts.SessionHandle.SetModel(newModel)
					} else if m.agent != nil {
						m.agent.SetModel(newModel)
					}
					m.opts.Model = newModel
					m.statusLine.SetModel(newModel)
					m.refreshThinkingLevel()
					if m.opts.SettingsManager != nil {
						_ = m.opts.SettingsManager.SetDefaultModelAndProvider(newModel.Provider.ID(), newModel.ID)
					}
					status = fmt.Sprintf("Logged in to Anthropic. Selected %s. Credentials saved to %s", newModel.ID, authPath)
				}
			}
			m.showStatus(status)
			m.appendToChat(tui.NewMarkdown("✓ " + status))
			m.updateProviderInfo()
			m.tuiInst.ForceFullRender()
			m.tuiInst.Render()
		})
	}()

	return nil
}

// runLoginGitHubCopilotDialog runs the user-invoked GitHub Copilot login flow
// with the upstream LoginDialog surface. Automatic 401 re-auth uses
// runLoginGitHubCopilot below because it can start from a turn goroutine where a
// modal input loop would be unsafe.
func (m *InteractiveMode) runLoginGitHubCopilotDialog(loginCtx context.Context) error {
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return fmt.Errorf("auth storage: %w", err)
	}

	loginCtx, loginCancel := context.WithCancel(loginCtx)
	dlg := tui.NewLoginDialog(buildAuthProviderName("github-copilot"), loginCancel)
	renderNotify := make(chan struct{}, 16)
	notify := func() {
		select {
		case renderNotify <- struct{}{}:
		default:
		}
	}

	cb := ai.CopilotLoginCallbacks{
		OnPrompt: func(promptCtx context.Context) (string, error) {
			ch := dlg.ShowInput("GitHub Enterprise URL/domain (blank for github.com)", "company.ghe.com")
			notify()
			select {
			case v, ok := <-ch:
				if !ok {
					return "", fmt.Errorf("Login cancelled")
				}
				return v, nil
			case <-promptCtx.Done():
				return "", promptCtx.Err()
			case <-loginCtx.Done():
				return "", loginCtx.Err()
			}
		},
		OnAuth: func(verificationURL, userCode string) {
			dlg.ShowAuth(verificationURL, fmt.Sprintf("Enter code: %s", userCode))
			notify()
			if !parityHarnessEnabled() {
				_ = openBrowser(verificationURL)
			}
		},
		OnProgress: func(msg string) {
			dlg.ShowProgress(msg)
			notify()
		},
	}

	var authPath string
	go func() {
		defer loginCancel()

		cred, err := loginGitHubCopilotForParity(loginCtx, cb)
		if err != nil {
			if loginCtx.Err() == nil {
				dlg.ShowProgress(fmt.Sprintf("Login failed: %v", err))
				notify()
			}
			return
		}

		if err := auth.Set("github-copilot", cred); err != nil {
			dlg.ShowProgress(fmt.Sprintf("Failed to store credentials: %v", err))
			notify()
			return
		}
		if m.opts.ModelRegistry != nil {
			m.opts.ModelRegistry.Refresh()
		}
		authPath = auth.Path()
		dlg.Success()
		notify()
	}()

	ok := m.runEditorSlotLoginDialog(dlg, renderNotify)
	if ok {
		status := fmt.Sprintf("Logged in to GitHub Copilot. Credentials saved to %s", authPath)
		m.showStatus(status)
		m.appendToChat(tui.NewMarkdown("✓ " + status))
		m.updateProviderInfo()
		m.tuiInst.ForceFullRender()
		m.tuiInst.Render()
	}
	return nil
}

// runLoginGitHubCopilot runs the GitHub Copilot device flow inline.
// Mirrors upstream flow: showAuth → poll → store credentials → refresh
// model registry → auto-select model → status message.
func (m *InteractiveMode) runLoginGitHubCopilot(loginCtx context.Context) error {
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return fmt.Errorf("auth storage: %w", err)
	}

	// Show progress in chat: mirrors upstream LoginDialogComponent.
	m.appendChatBlock(tui.NewMarkdown("**Login to GitHub Copilot**"))

	// Run login in a goroutine so the TUI stays responsive during the
	// device code polling window (~60s). Mirrors upstream's async
	// showLoginDialog (interactive-mode.ts:4325-4444).
	// Use abortCtx so Ctrl+C during polling cancels the login.
	loginCtx, loginCancel := context.WithCancel(loginCtx)
	loginToken := m.beginLogin(loginCancel)

	cb := ai.CopilotLoginCallbacks{
		OnPrompt: func(_ context.Context) (string, error) {
			// Automatic reauthentication uses github.com because this path has no enterprise-domain input surface.
			return "", nil
		},
		OnAuth: func(verificationURL, userCode string) {
			msg := fmt.Sprintf(
				"1. Open: %s\n2. Enter code: **%s**\n3. Authorize, then come back here.\n\nWaiting for authorization... (Ctrl+C to cancel)",
				verificationURL, userCode)
			m.runOnMain(m.runCtx, func() {
				m.appendChatBlock(tui.NewMarkdown(msg))
				m.tuiInst.Render()
			})

			// Try to open browser: mirrors upstream LoginDialogComponent.
			_ = openBrowser(verificationURL)
		},
		OnProgress: func(msg string) {
			m.runOnMain(m.runCtx, func() {
				if m.statusLine != nil {
					m.statusLine.Flash(msg, 5*time.Second)
				}
				m.tuiInst.Render()
			})
		},
	}

	go func() {
		defer loginCancel()
		defer m.endLogin(loginToken)

		cred, err := ai.LoginGitHubCopilot(loginCtx, cb)
		if err != nil {
			m.runOnMain(m.runCtx, func() {
				if loginCtx.Err() != nil {
					// Cancelled: silent, mirrors upstream dialog.signal abort.
					m.appendChatBlock(tui.NewMarkdown("Login cancelled."))
				} else {
					m.appendChatBlock(tui.NewMarkdown(fmt.Sprintf("Login failed: %v", err)))
				}
				m.tuiInst.ForceFullRender()
				m.tuiInst.Render()
			})
			return
		}

		if err := auth.Set("github-copilot", cred); err != nil {
			m.runOnMain(m.runCtx, func() {
				m.appendChatBlock(tui.NewMarkdown(fmt.Sprintf("Failed to store credentials: %v", err)))
				m.tuiInst.ForceFullRender()
				m.tuiInst.Render()
			})
			return
		}

		// Success: mirror upstream's post-login flow:
		// refresh model registry → auto-select model → status message.
		// (interactive-mode.ts:4394-4438)
		if m.opts.ModelRegistry != nil {
			m.opts.ModelRegistry.Refresh()
		}

		// Model selection + status touch shared state the main loop reads.
		m.runOnMain(m.runCtx, func() {
			authPath := auth.Path()
			status := fmt.Sprintf("Logged in to GitHub Copilot. Credentials saved to %s", authPath)

			// Auto-select a model if none is currently set.
			// Mirrors upstream isUnknownModel check (interactive-mode.ts:4398).
			if m.opts.Model == nil && m.opts.ModelBuilder != nil {
				if newModel, err := m.opts.ModelBuilder("github-copilot/gpt-4o"); err == nil {
					if m.opts.SessionHandle != nil {
						_ = m.opts.SessionHandle.SetModel(newModel)
					} else if m.agent != nil {
						m.agent.SetModel(newModel)
					}
					m.opts.Model = newModel
					m.statusLine.SetModel(newModel)
					m.refreshThinkingLevel()
					if m.opts.SettingsManager != nil {
						_ = m.opts.SettingsManager.SetDefaultModelAndProvider(newModel.Provider.ID(), newModel.ID)
					}
					status = fmt.Sprintf("Logged in to GitHub Copilot. Selected %s. Credentials saved to %s", newModel.ID, authPath)
				}
			}

			if m.statusLine != nil {
				m.statusLine.Flash(status, 5*time.Second)
			}
			m.appendToChat(tui.NewMarkdown("✓ " + status))
			m.updateProviderInfo()
			// Force full repaint: the goroutine added chat content AND changed
			// the status line (provider count, subscription), which can confuse
			// the differential renderer and leave artifacts in the footer area.
			m.tuiInst.ForceFullRender()
			m.tuiInst.Render()
		})
	}()

	return nil
}

// runOAuthLogout removes stored OAuth credentials for a provider.
// Mirrors upstream showOAuthSelector logout branch (interactive-mode.ts:4296-4308).
func (m *InteractiveMode) runOAuthLogout(provider string) error {
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return fmt.Errorf("auth storage: %w", err)
	}

	deleted := false
	if _, ok, _ := auth.Get(provider); ok {
		if err := auth.Delete(provider); err != nil {
			return fmt.Errorf("logout: %w", err)
		}
		deleted = true
	}
	// Upstream logout deletes through RuntimeCredentials, which also drops
	// the provider's --api-key runtime key.
	if m.opts.ModelRegistry != nil {
		if _, ok := m.opts.ModelRegistry.RuntimeAPIKey(provider); ok {
			m.opts.ModelRegistry.RemoveRuntimeAPIKey(provider)
			deleted = true
		}
	}
	if store, ok := oauthCredentialStore(provider); ok {
		removed, err := store.DeleteOAuthCredentials()
		if err != nil {
			return fmt.Errorf("logout: %w", err)
		}
		deleted = deleted || removed
	}
	if !deleted {
		if m.statusLine != nil {
			m.statusLine.Flash(fmt.Sprintf("No credentials stored for %q. Use /login first.", provider), 3*time.Second)
		}
		return nil
	}

	m.updateProviderInfo()
	m.tuiInst.ForceFullRender()
	m.tuiInst.Render()
	return nil
}

// applyEditorMaxVisible sets the editor's max visible visual-line cap
// to `max(5, floor(terminalRows * 0.3))`, matching upstream editor.ts
// (.upstream/v0.69.0/packages/tui/src/components/editor.ts:425).
// Called on startup and SIGWINCH.
func (m *InteractiveMode) applyEditorMaxVisible() {
	if m.editor == nil || m.tuiInst == nil {
		return
	}
	rows := m.tuiInst.Height()
	if rows <= 0 {
		rows = 30
	}
	cap := max(rows*30/100, 5)
	m.editor.SetMaxVisibleLines(cap)
}

// updateProviderInfo updates the footer from the available model snapshot or the active scope, including dynamically registered providers such as Radius. It does not refresh catalogs.
func (m *InteractiveMode) updateProviderInfo() {
	if m.statusLine == nil {
		return
	}
	items := m.availableModelItems()
	if scoped := m.scopedModelItems(items); len(scoped) > 0 {
		items = scoped
	}
	providers := make(map[string]struct{})
	for _, item := range items {
		providers[item.Provider] = struct{}{}
	}
	m.statusLine.SetProviderCount(len(providers))

	m.statusLine.SetUsingSubscription(m.footerUsingSubscription(m.opts.Model))
}

// footerUsingSubscription mirrors footer.ts: Kimi Coding is
// subscription-backed despite API-key authentication; any other provider
// needs a stored OAuth login whose provider is a subscription login.
func (m *InteractiveMode) footerUsingSubscription(model *ai.Model) bool {
	if model == nil {
		return false
	}
	providerID := model.ProviderMeta.ProviderID
	if providerID == "" && model.Provider != nil {
		providerID = model.Provider.ID()
	}
	if providerID == "kimi-coding" {
		return true
	}
	if !ai.IsOAuthSubscriptionProvider(providerID) {
		return false
	}
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return false
	}
	cred, ok, err := auth.Get(providerID)
	return ok && err == nil && cred.Type == ai.CredentialOAuth
}

// newFooter builds the footer bound to the session's stored usage totals and
// to the active model's subscription marker.
func (m *InteractiveMode) newFooter() *StatusLine {
	footer := NewStatusLine(m.opts.Model, "", nil)
	footer.SetUsageTotalsSource(m.footerUsageTotals)
	footer.SetSubscriptionResolver(m.footerUsingSubscription)
	return footer
}

// footerUsageTotals reads the current session's all-entry usage totals.
func (m *InteractiveMode) footerUsageTotals() footerUsageTotals {
	session := m.currentSession()
	if session == nil {
		return footerUsageTotals{}
	}
	return session.FooterUsageTotals()
}

// openBrowser opens a URL in the default browser.
// Mirrors upstream LoginDialogComponent (login-dialog.ts:122-124).
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default: // linux, freebsd, etc.
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start()
}

func formatProviderErrorForDisplay(stopReason, raw string) (statusText, chatText string) {
	clean := compactProviderError(raw)
	if strings.Contains(clean, "github-copilot") && (strings.Contains(clean, "Bad credentials") || strings.Contains(clean, "HTTP 401") || strings.Contains(clean, "token refresh failed")) {
		// Mirrors upstream agent-session.ts: name both possible causes rather
		// than asserting expiry, since a refresh also fails on rate limits,
		// network loss, and provider outages. The provider's own text renders
		// in the assistant block below and carries the specific reason.
		return "GitHub Copilot authentication failed: credentials may have expired or network is unavailable; run pig login", clean
	}
	verb := "failed"
	if stopReason == "aborted" {
		verb = "aborted"
	}
	statusText = "Provider request " + verb
	chatText = clean
	if len(chatText) > 360 {
		chatText = chatText[:357] + "..."
	}
	return statusText, chatText
}

func compactProviderError(raw string) string {
	clean := strings.TrimSpace(raw)
	clean = strings.ReplaceAll(clean, "\r\n", " ")
	clean = strings.ReplaceAll(clean, "\n", " ")
	clean = strings.Join(strings.Fields(clean), " ")
	return clean
}

// finalizeRunningTools freezes every tool component still in ToolStateRunning,
// stopping its live "Elapsed X.Xs" footer from recomputing time.Since(start) on
// every subsequent render. While a tool block stays running after it has
// scrolled above the viewport, each recompute changes a line the differential
// renderer cannot reach in place, forcing a full clearing repaint (the flicker
// seen after aborting a long-running tool). Called from the Esc abort dispatch
// and from agent_end; idempotent because FinalizeAborted no-ops once a tool is
// terminal, and a genuine ToolExecutionEnd arriving later still overwrites the
// frozen placeholder with the real result via SetResult.
