package codingagent

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
	"github.com/MichaelKinsy/PiG/tui"
)

// catalogProviderName is the name of a catalog provider (providers/<id>.ts), or "" for a provider outside the catalog, whose name its registration or OAuth flow declares.
func catalogProviderName(id string) string {
	if slices.Contains(ai.GeneratedProviders, id) {
		return ai.ProviderDisplayName(id)
	}
	return ""
}

// authSelectorProviderName is the name the login selector shows for an OAuth provider: upstream's provider.name
// (interactive-mode.ts getLoginProviderOptions), which for a catalog provider is the catalog's name
// (providers/<id>.ts) rather than the OAuth flow's own name ("Meta (Muse subscription)"). A provider outside the
// catalog, such as an extension's OAuth provider, keeps the name its flow declares.
func authSelectorProviderName(provider ai.OAuthProviderInterface) string {
	if name := catalogProviderName(provider.ID()); name != "" {
		return name
	}
	return provider.Name()
}

// composedLoginProvider returns the request runtime's composition of a provider, or nil when the runtime does not compose it. Upstream lists login options from the runtime's providers (interactive-mode.ts:5712-5745), so the composition is the authority for the auth methods and name of a provider it composes.
func (m *InteractiveMode) composedLoginProvider(id string) *RuntimeProvider {
	runtime := m.opts.RequestAuthRuntime
	if runtime == nil {
		return nil
	}
	providers := runtime.GetProviders()
	if index := slices.IndexFunc(providers, func(composed *RuntimeProvider) bool { return composed.ID == id }); index >= 0 {
		return providers[index]
	}
	return nil
}

// loginProviderSubscription reports whether a provider's OAuth sign-in is backed by a subscription: upstream's provider.auth.oauth?.isSubscription === true (interactive-mode.ts:5722, 5756).
func (m *InteractiveMode) loginProviderSubscription(id string) *bool {
	auth := m.providerAuth(id)
	if composed := m.composedLoginProvider(id); composed != nil {
		auth = composed.Auth
	}
	subscription := auth.OAuth != nil && auth.OAuth.IsSubscription
	return &subscription
}

// oauthProviderList returns the auth providers available to /login and /logout.
func (m *InteractiveMode) oauthProviderList(mode string, includeStatus ...bool) []tui.OAuthProvider {
	if mode == "logout" {
		providers, _ := m.getLogoutProviderOptions()
		return providers
	}
	var all []tui.OAuthProvider
	for _, provider := range m.oauthProviders() {
		// pig additive (D92): a provider whose catalog models are all on stripped APIs offers no model to log in for.
		if ai.ProviderStripped(provider.ID()) {
			continue
		}
		name := authSelectorProviderName(provider)
		// The runtime is the authority for a provider it composes: without an OAuth method there is no account login, and its composed name (a models.json `name` over the catalog name, provider-composer.ts:588) is the provider.name upstream lists.
		if composed := m.composedLoginProvider(provider.ID()); composed != nil {
			if composed.Auth.OAuth == nil {
				continue
			}
			if composed.Name != "" {
				name = composed.Name
			}
		}
		all = append(all, tui.OAuthProvider{ID: provider.ID(), Name: name, AuthType: "oauth"})
	}
	slices.SortFunc(all, func(a, b tui.OAuthProvider) int {
		return strings.Compare(a.Name, b.Name)
	})
	if mode == "login-api-key" {
		// The API-key list includes providers that also expose OAuth.
		all = nil
		for _, p := range ai.APIKeyProviders() {
			// A provider the runtime composes without an API-key method has no API-key login (interactive-mode.ts:5732).
			if composed := m.composedLoginProvider(p.ID); composed != nil && composed.Auth.APIKey == nil {
				continue
			}
			all = append(all, tui.OAuthProvider{ID: p.ID, Name: p.Name, AuthType: "api_key"})
		}
		all = m.withNativeAPIKeyProviders(all)
	}
	for i := range all {
		method := m.providerAuth(all[i].ID)
		if all[i].AuthType == "api_key" && method.APIKey != nil {
			all[i].Method = method.APIKey
		}
		if all[i].AuthType == "oauth" && method.OAuth != nil {
			all[i].Method = method.OAuth
		}
	}
	if len(includeStatus) > 0 && !includeStatus[0] {
		return all
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
			all[i].Status = &ai.AuthCheck{Type: c.Type, Source: string(ai.AuthSourceStored)}
		}
		if all[i].Status == nil {
			if store, ok := oauthCredentialStore(all[i].ID); ok {
				if status, ok := store.OAuthCredentialStatus(); ok {
					all[i].Status = &ai.AuthCheck{Type: ai.CredentialType(status.AuthType), Source: status.Source}
				}
			}
		}
		if all[i].Status == nil {
			status := auth.GetAuthStatus(all[i].ID)
			status.Configured = status.Source != ""
			if m.opts.ModelRegistry != nil {
				status = m.opts.ModelRegistry.GetProviderAuthStatus(all[i].ID)
			}
			all[i].Status = authSelectorStatus(status, false)
		}
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

func (m *InteractiveMode) maskSecretInput() bool {
	if m.opts.SettingsManager != nil {
		return m.opts.SettingsManager.Get().GetMaskSecretInput()
	}
	return m.opts.Settings.GetMaskSecretInput()
}

func (m *InteractiveMode) newLoginDialog(name string, cancel func(), titleOverride ...string) *tui.LoginDialogComponent {
	dialog := tui.NewLoginDialogComponent(m.tuiInst, name, func(bool, string) {
		if cancel != nil {
			cancel()
		}
	}, "", titleOverride...)
	dialog.SetMaskSecretInput(m.maskSecretInput())
	dialog.SetCopyToClipboard(copyToClipboard, m.requestRender)
	return dialog
}

// reportLoginBlocked reports the login dialog as blocked on the user until the returned function runs (interactive-mode.ts:6321, :6334).
func (m *InteractiveMode) reportLoginBlocked(providerName string) func() {
	m.programStatusReporter().SetBlocked("login", &BlockedStatus{Kind: tui.ProgramStatusKindAuth, Message: "Log in to " + providerName})
	return func() { m.programStatusReporter().SetBlocked("login", nil) }
}

// runOAuthLogin runs the provider's interactive OAuth dialog.
func (m *InteractiveMode) runOAuthLogin(loginCtx context.Context, loginProvider tui.OAuthProvider) error {
	provider := loginProvider.ID
	switch provider {
	case "github-copilot":
		defer m.reportLoginBlocked(loginProvider.Name)()
		return m.runLoginGitHubCopilotDialog(loginCtx)
	case "openai-codex":
		defer m.reportLoginBlocked(loginProvider.Name)()
		return m.runLoginOpenAICodex(loginCtx)
	default:
		oauthProvider, ok := m.lookupOAuthProvider(provider)
		if !ok {
			return fmt.Errorf("unknown OAuth provider %q", provider)
		}
		method, selected := m.selectOAuthLoginMethod(oauthProvider)
		if !selected {
			return errLoginCancelled
		}
		defer m.reportLoginBlocked(loginProvider.Name)()
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

// oauthInteractionLogin is a provider object's own auth.oauth.login, which takes Pi's AuthInteraction rather than the login callbacks.
type oauthInteractionLogin interface {
	LoginInteraction(ctx context.Context, interaction ai.AuthInteraction) (ai.OAuthCredentials, error)
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
	selector := tui.NewExtensionSelectorComponent(prompt.Message, labels, nil, nil)
	// Pi introduces Radius in its sign-in method prompt (interactive-mode.ts:6194).
	if provider.ID() == RadiusProviderID {
		selector.SetDescription(radiusLoginIntro)
	}
	index, ok := m.runEditorSlotExtensionSelector(selector)
	if !ok || index < 0 || index >= len(prompt.Options) {
		return "", false
	}
	return prompt.Options[index].ID, true
}

func runOAuthProviderLogin(ctx context.Context, provider ai.OAuthProviderInterface, callbacks ai.OAuthLoginCallbacks, interaction ai.AuthInteraction) (ai.OAuthCredentials, error) {
	if native, ok := provider.(oauthInteractionLogin); ok {
		return native.LoginInteraction(ctx, interaction)
	}
	if contextual, ok := provider.(oauthContextLogin); ok {
		return contextual.LoginContext(ctx, callbacks)
	}
	return provider.Login(callbacks)
}

func (m *InteractiveMode) runLoginRegisteredOAuth(loginCtx context.Context, provider ai.OAuthProviderInterface, selectedMethod string) error {
	previousModel := m.opts.Model
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return fmt.Errorf("auth storage: %w", err)
	}

	loginCtx, loginCancel := context.WithCancel(loginCtx)
	providerName := buildAuthProviderName(provider.ID())
	if providerName == provider.ID() {
		providerName = provider.Name()
	}
	dlg := m.newLoginDialog(providerName, loginCancel)
	renderNotify := make(chan struct{}, 16)
	notify := func() {
		select {
		case renderNotify <- struct{}{}:
		default:
		}
	}
	selects := make(chan authPromptRequest)
	// cancelled closes when the flow itself ends with "Login cancelled", as it does after the user cancels its select prompt.
	cancelled := make(chan struct{})

	prompt := func(ctx context.Context, value ai.OAuthPrompt) (string, error) {
		ch := dlg.ShowPrompt(value.Message, value.Placeholder)
		notify()
		extension.CallInitiated(ctx)
		select {
		case input, ok := <-ch:
			if !ok {
				return "", fmt.Errorf("Login cancelled")
			}
			// upstream: login-dialog.ts:58-66 showPrompt resolves the submitted text, empty or not; no prompt enforces allowEmpty.
			return input, nil
		case <-ctx.Done():
			return "", errLoginCancelled
		case <-loginCtx.Done():
			return "", loginCtx.Err()
		}
	}
	const manualCodeMessage = "Paste redirect URL below, or complete login in browser:"
	manualCode := func(ctx context.Context, message string) (string, error) {
		ch := dlg.ShowManualInput(message)
		notify()
		extension.CallInitiated(ctx)
		return awaitLoginDialogInput(ctx, loginCtx, ch)
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
		options := make([]ai.AuthSelectOption, len(value.Options))
		for i, option := range value.Options {
			options[i] = ai.AuthSelectOption{ID: option.ID, Label: option.Label}
		}
		// Pi answers a select prompt with a selector in place of the dialog (interactive-mode.ts:6243-6276 showAuthSelect); the dialog loop marks the call initiated once the selector is mounted.
		request := authPromptRequest{ctx: ctx, prompt: ai.AuthSelectPrompt{Message: value.Message, Options: options}, reply: make(chan authPromptReply, 1)}
		select {
		case selects <- request:
		case <-ctx.Done():
			extension.CallInitiated(ctx)
			return "", errLoginCancelled
		case <-loginCtx.Done():
			extension.CallInitiated(ctx)
			return "", loginCtx.Err()
		}
		select {
		case reply := <-request.reply:
			return reply.value, reply.err
		case <-ctx.Done():
			return "", errLoginCancelled
		case <-loginCtx.Done():
			return "", loginCtx.Err()
		}
	}

	cb := ai.OAuthLoginCallbacks{
		GetDeviceID:     m.loginDeviceID,
		OnPrompt:        func(value ai.OAuthPrompt) (string, error) { return prompt(context.Background(), value) },
		OnPromptContext: prompt,
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			showDeviceCode(dlg, tui.OAuthDeviceCodeInfo{UserCode: info.UserCode, VerificationURI: info.VerificationURI, IntervalSeconds: info.IntervalSeconds, ExpiresInSeconds: info.ExpiresInSeconds})
			notify()
		},
		OnAuth: func(info ai.OAuthAuthInfo) {
			dlg.ShowAuth(info.URL, info.Instructions)
			notify()
			if !parityHarnessEnabled() {
				_ = openBrowser(info.URL)
			}
		},
		OnManualCodeInput:        func() (string, error) { return manualCode(context.Background(), manualCodeMessage) },
		OnManualCodeInputContext: func(ctx context.Context) (string, error) { return manualCode(ctx, manualCodeMessage) },
		// Pi shows a flow's own manual_code message (interactive-mode.ts:6207-6208).
		OnManualCodePromptContext: func(ctx context.Context, value ai.AuthManualCodePrompt) (string, error) {
			return manualCode(ctx, value.Message)
		},
		OnProgress: func(msg string) {
			dlg.ShowProgress(msg)
			notify()
		},
		OnSelect:        func(value ai.OAuthSelectPrompt) (string, error) { return selectMethod(context.Background(), value) },
		OnSelectContext: selectMethod,
	}
	// A provider object's login prompts and notifies through Pi's AuthInteraction (interactive-mode.ts:6315-6336 loginProvider). Its prompts reach the dialog as showAuthPrompt shows them and its events as notifyAuthDialog shows them, an info event with its links (:6278-6313).
	interaction := ai.AuthInteraction{
		Prompt: func(ctx context.Context, value ai.AuthPrompt) (string, error) {
			switch p := value.(type) {
			case ai.AuthSelectPrompt:
				options := make([]ai.OAuthSelectOption, len(p.Options))
				for i, option := range p.Options {
					options[i] = ai.OAuthSelectOption{ID: option.ID, Label: option.Label}
				}
				return selectMethod(ctx, ai.OAuthSelectPrompt{Message: p.Message, Options: options})
			case ai.AuthManualCodePrompt:
				return manualCode(ctx, p.Message)
			case ai.AuthTextPrompt:
				return prompt(ctx, ai.OAuthPrompt{Message: p.Message, Placeholder: p.Placeholder})
			case ai.AuthSecretPrompt:
				return prompt(ctx, ai.OAuthPrompt{Message: p.Message, Placeholder: p.Placeholder})
			default:
				return "", fmt.Errorf("unknown login prompt type %q", value.Type())
			}
		},
		Notify: func(event ai.AuthEvent) {
			switch e := event.(type) {
			case ai.AuthURLEvent:
				cb.OnAuth(ai.OAuthAuthInfo(e))
			case ai.AuthDeviceCodeEvent:
				info := ai.OAuthDeviceCodeInfo{UserCode: e.UserCode, VerificationURI: e.VerificationURI}
				if e.IntervalSeconds != nil {
					info.IntervalSeconds = *e.IntervalSeconds
				}
				if e.ExpiresInSeconds != nil {
					info.ExpiresInSeconds = *e.ExpiresInSeconds
				}
				cb.OnDeviceCode(info)
			case ai.AuthInfoEvent:
				links := make([]tui.AuthInfoLink, len(e.Links))
				for i, link := range e.Links {
					links[i] = tui.AuthInfoLink{Label: link.Label, URL: link.URL}
				}
				dlg.ShowInfo(e.Message, links, false)
				notify()
			case ai.AuthProgressEvent:
				cb.OnProgress(e.Message)
			}
		},
	}

	authPath := auth.Path()
	var failed atomic.Bool
	go func() {
		defer loginCancel()

		cred, err := runOAuthProviderLogin(loginCtx, provider, cb, interaction)
		if err != nil {
			// upstream: interactive-mode.ts:6365-6366 (showLoginDialog) returns to the previous menu when the login rejects with "Login cancelled".
			//portlint:allow erroridentity Pi compares the message, and an extension's rejection crosses the wire as text with no error identity
			if loginCtx.Err() == nil && err.Error() == errLoginCancelled.Error() {
				close(cancelled)
				return
			}
			if loginCtx.Err() == nil {
				failed.Store(true)
				dlg.ShowProgress(fmt.Sprintf("Login failed: %v", err))
				notify()
			}
			return
		}

		if store, ok := provider.(ai.OAuthCredentialStore); ok {
			// pig additive (D40): a contributed credential store reports its own saved location.
			authPath, err = store.StoreOAuthCredentials(cred)
		} else {
			var credential ai.Credential
			if credential, err = ai.CredentialFromOAuth(cred); err == nil {
				err = saveLoginCredential(loginCtx, auth, provider.ID(), credential)
			}
		}
		if err != nil {
			failed.Store(true)
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

	ok := m.runEditorSlotLoginDialogPrompts(dlg, renderNotify, selects, cancelled)
	select {
	case <-cancelled:
		return errLoginCancelled
	default:
	}
	if ok {
		if m.synchronizeLoginCredential(m.loginContext(), provider.ID(), providerName, ai.CredentialOAuth, dlg.Redact) {
			return nil
		}
		m.completeProviderAuthentication(provider.ID(), providerName, ai.CredentialOAuth, previousModel, authPath, dlg.Redact)
		// Pi offers the Radius MCP server after a Radius sign-in (interactive-mode.ts:6276).
		if provider.ID() == RadiusProviderID {
			m.offerRadiusMcpServer(provider.ID(), providerName)
		}
		return nil
	}
	return loginDialogOutcome(dlg, &failed, providerName)
}

// awaitLoginDialogInput waits for a login dialog answer. A prompt whose own context ends, as a flow's manual code prompt
// does when the browser callback completes the login first, rejects with "Login cancelled", as Pi's showAuthPrompt races
// the answer against prompt.signal (interactive-mode.ts:6212-6222).
func awaitLoginDialogInput(ctx, loginCtx context.Context, answer <-chan string) (string, error) {
	select {
	case input, ok := <-answer:
		if !ok {
			return "", errLoginCancelled
		}
		return input, nil
	case <-ctx.Done():
		return "", errLoginCancelled
	case <-loginCtx.Done():
		return "", loginCtx.Err()
	}
}

// loginDialogOutcome reports a login dialog the user closed before the login failed. Pi's dialog cancel aborts the
// login signal first, so the login fails with the abort error and Pi shows "Failed to login to <name>: This operation
// was aborted" instead of reopening a menu (login-dialog.ts:83-90, interactive-mode.ts:6287-6288; probed against Pi
// 1.0.0 with Escape at the Anthropic browser login).
func loginDialogOutcome(dlg *tui.LoginDialogComponent, failed *atomic.Bool, providerName string) error {
	if dlg.Cancelled() && !failed.Load() {
		return fmt.Errorf("Failed to login to %s: %w", providerName, errLoginAborted)
	}
	return nil
}

// loginOpenAICodex is the Codex flow runLoginOpenAICodex starts; tests replace it to observe the callbacks.
var loginOpenAICodex = ai.LoginOpenAICodex

// runLoginOpenAICodex runs the OpenAI Codex (ChatGPT) OAuth flow.
// Mirrors upstream openai-codex.ts login(), which first presents a method
// selector (browser vs device-code) via onSelect, then runs the chosen flow.
// The browser path uses the PKCE + localhost callback dialog; the device-code
// path (RFC 8628) shows the user code while polling.
func (m *InteractiveMode) runLoginOpenAICodex(loginCtx context.Context) error {
	previousModel := m.opts.Model
	// Method selector, matching upstream openai-codex.ts login()'s onSelect call.
	methodSel := tui.NewExtensionSelectorComponent("Select OpenAI Codex login method:", []string{
		"Browser login (default)",
		"Device code login (headless)",
	}, nil, nil)
	methodIdx, methodOK := m.runEditorSlotExtensionSelector(methodSel)
	if !methodOK {
		return errLoginCancelled
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
	dlg := m.newLoginDialog(buildAuthProviderName("openai-codex"), loginCancel)
	renderNotify := make(chan struct{}, 16)
	notify := func() {
		select {
		case renderNotify <- struct{}{}:
		default:
		}
	}

	cb := ai.OAuthLoginCallbacks{
		GetDeviceID: m.loginDeviceID,
		OnSelect: func(ai.OAuthSelectPrompt) (string, error) {
			return loginMethod, nil
		},
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			showDeviceCode(dlg, tui.OAuthDeviceCodeInfo{UserCode: info.UserCode, VerificationURI: info.VerificationURI, IntervalSeconds: info.IntervalSeconds, ExpiresInSeconds: info.ExpiresInSeconds})
			notify()
		},
		OnAuth: func(info ai.OAuthAuthInfo) {
			dlg.ShowAuth(info.URL, info.Instructions)
			notify()
			if !parityHarnessEnabled() {
				_ = openBrowser(info.URL)
			}
		},
		OnManualCodeInput: func() (string, error) {
			ch := dlg.ShowManualInput("Paste redirect URL below, or complete login in browser:")
			notify()
			return awaitLoginDialogInput(context.Background(), loginCtx, ch)
		},
		// Pi shows a flow's own manual_code message (interactive-mode.ts:6207-6208).
		OnManualCodePromptContext: func(ctx context.Context, value ai.AuthManualCodePrompt) (string, error) {
			ch := dlg.ShowManualInput(value.Message)
			notify()
			return awaitLoginDialogInput(ctx, loginCtx, ch)
		},
		OnProgress: func(msg string) {
			dlg.ShowProgress(msg)
			notify()
		},
	}

	var failed atomic.Bool
	go func() {
		defer loginCancel()

		cred, err := loginOpenAICodex(loginCtx, cb)
		if err != nil {
			if loginCtx.Err() == nil {
				failed.Store(true)
				dlg.ShowProgress(fmt.Sprintf("Login failed: %v", err))
				notify()
			}
			return
		}

		codexCred, err := ai.CredentialFromOAuth(cred)
		if err == nil {
			err = saveLoginCredential(loginCtx, auth, "openai-codex", codexCred)
		}
		if err != nil {
			failed.Store(true)
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
	// Back on the main input-loop goroutine (dialog closed). Apply post-login UI
	// here rather than from the login goroutine: during the dialog the main
	// goroutine ran runEditorSlotLoginDialog's own loop, not the inputLoop
	// select, so it did not drain uiTaskCh and a runOnMain post would deadlock.
	// ok == !Cancelled() is true only when the goroutine reached dlg.Success().
	if ok {
		if m.synchronizeLoginCredential(m.loginContext(), "openai-codex", buildAuthProviderName("openai-codex"), ai.CredentialOAuth, dlg.Redact) {
			return nil
		}
		m.completeProviderAuthentication("openai-codex", buildAuthProviderName("openai-codex"), ai.CredentialOAuth, previousModel, auth.Path(), dlg.Redact)
		return nil
	}
	return loginDialogOutcome(dlg, &failed, buildAuthProviderName("openai-codex"))
}

// runLoginGitHubCopilotDialog runs the GitHub Copilot login flow in the editor slot.
func (m *InteractiveMode) runLoginGitHubCopilotDialog(loginCtx context.Context) error {
	previousModel := m.opts.Model
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return fmt.Errorf("auth storage: %w", err)
	}

	loginCtx, loginCancel := context.WithCancel(loginCtx)
	dlg := m.newLoginDialog(buildAuthProviderName("github-copilot"), loginCancel)
	renderNotify := make(chan struct{}, 16)
	notify := func() {
		select {
		case renderNotify <- struct{}{}:
		default:
		}
	}

	cb := ai.CopilotLoginCallbacks{
		OnPrompt: func(promptCtx context.Context) (string, error) {
			ch := dlg.ShowPrompt("GitHub Enterprise URL/domain (blank for github.com)", "company.ghe.com")
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
			showDeviceCode(dlg, tui.OAuthDeviceCodeInfo{UserCode: userCode, VerificationURI: verificationURL})
			notify()
		},
		OnProgress: func(msg string) {
			dlg.ShowProgress(msg)
			notify()
		},
	}

	var failed atomic.Bool
	go func() {
		defer loginCancel()

		cred, err := loginGitHubCopilotForParity(loginCtx, cb)
		if err != nil {
			if loginCtx.Err() == nil {
				failed.Store(true)
				dlg.ShowProgress(fmt.Sprintf("Login failed: %v", err))
				notify()
			}
			return
		}

		if err := saveLoginCredential(loginCtx, auth, "github-copilot", cred); err != nil {
			failed.Store(true)
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
		if m.synchronizeLoginCredential(m.loginContext(), "github-copilot", buildAuthProviderName("github-copilot"), ai.CredentialOAuth, dlg.Redact) {
			return nil
		}
		m.completeProviderAuthentication("github-copilot", buildAuthProviderName("github-copilot"), ai.CredentialOAuth, previousModel, auth.Path(), dlg.Redact)
		return nil
	}
	return loginDialogOutcome(dlg, &failed, buildAuthProviderName("github-copilot"))
}

// runOAuthLogout removes stored OAuth credentials for a provider.
// Mirrors upstream showOAuthSelector logout branch (interactive-mode.ts:4296-4308).
func (m *InteractiveMode) runOAuthLogout(ctx context.Context, provider string) error {
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		return fmt.Errorf("auth storage: %w", err)
	}

	deleted := false
	if _, ok, _ := auth.Get(provider); ok {
		if err := auth.Delete(ctx, provider); err != nil {
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
	// upstream: packages/coding-agent/src/core/model-runtime.ts:logout synchronizes after every logout; a failure keeps the committed removal.
	if runtime := m.opts.RequestAuthRuntime; runtime != nil {
		if err := runtime.SynchronizeCredentialState(ctx, provider, CredentialSynchronizationLogout, nil); err != nil {
			return err
		}
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
func (m *InteractiveMode) newFooter() *FooterComponent {
	footer := NewFooterComponentForSession(m, NewFooterDataProvider())
	footer.SetSubscriptionResolver(m.footerUsingSubscription)
	footer.SetModel(m.opts.Model)
	return footer
}

// routedModelHandle is implemented by a session handle whose session can route a virtual model selection.
type routedModelHandle interface {
	RoutedModelSelection() *RoutedModelSelection
}

// SessionManager is the current session's log (AgentSession.sessionManager); the mode is the footer's session so that the footer follows a
// Session replacement.
func (m *InteractiveMode) SessionManager() *Session { return m.currentSession() }

// RoutedModelSelection reads the physical model the current session's latest response was routed to (AgentSession.routedModel).
func (m *InteractiveMode) RoutedModelSelection() *RoutedModelSelection {
	if handle, ok := m.opts.SessionHandle.(routedModelHandle); ok {
		return handle.RoutedModelSelection()
	}
	return nil
}

// footerUsageTotals reads the current session's all-entry usage totals.
func (m *InteractiveMode) footerUsageTotals() footerUsageTotals {
	session := m.currentSession()
	if session == nil {
		return footerUsageTotals{}
	}
	return session.FooterUsageTotals()
}

// showDeviceCode shows a device-code login and waits, as Pi's notifyAuthDialog does for a device_code event (interactive-mode.ts:6112-6114). Pi opens a browser only for an auth URL, never for a device code.
func showDeviceCode(dlg *tui.LoginDialogComponent, info tui.OAuthDeviceCodeInfo) {
	dlg.ShowDeviceCode(info)
	dlg.ShowWaiting("Waiting for authentication...")
}

// OpenBrowser opens target in the platform browser. The launch is best-effort, as upstream's: callers still present the target to the user, so a launcher failure is not reported.
// Ports packages/coding-agent/src/utils/open-browser.ts
func OpenBrowser(target string) { _ = openBrowser(target) }

// browserCommand is the launcher command of upstream's openBrowser: no shell, the target as one argument, and Node's
// spawn option `detached: true` (see detachLauncher) with the standard files on the NUL device (`stdio: "ignore"`).
// On Windows cmd /c start would re-parse &, |, ^ in the URL, truncating OAuth URLs and running commands carried by a
// server-supplied device-code URI, so rundll32 receives the URL as one argument.
// Ports packages/coding-agent/src/utils/open-browser.ts
func browserCommand(target string) *exec.Cmd {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{target}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", target}
	default: // linux, freebsd, etc.
		cmd = "xdg-open"
		args = []string{target}
	}
	command := exec.Command(cmd, args...)
	// Upstream's spawn finds the launcher with libuv's search on Windows.
	nodespawn.SetProgram(command)
	detachLauncher(command)
	return command
}

// openBrowser starts the launcher and leaves it running on its own: it is detached from PiG's session or process
// group, so a terminal interrupt aimed at PiG does not reach it, and it is reaped when it exits (upstream's `unref`).
// A launcher that cannot start returns its error, which callers drop because they still present the target.
var openBrowser = func(target string) error {
	command := browserCommand(target)
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
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

// saveLoginCredential stores the credential a login produced as Pi's Models.login does, through credentials.modify (pi-ai models.ts:593-610): the cancellable auth lock waits up to 30 seconds for another process's lock and ignores a failed release.
func saveLoginCredential(ctx context.Context, auth *ai.AuthStorage, providerID string, credential ai.Credential) error {
	_, err := auth.Modify(ctx, providerID, func(*ai.Credential) (*ai.Credential, error) { return &credential, nil })
	return err
}

// loginDeviceID is the installation's device ID, which Pi passes to every login (interactive-mode.ts:6297) and Sign in
// with ChatGPT sends as its agent host ID.
func (m *InteractiveMode) loginDeviceID() string {
	if m.opts.SettingsManager == nil {
		return ""
	}
	return m.opts.SettingsManager.GetOrCreateDeviceID()
}

// authSelectorStatus is a login option's status: nil unless auth is configured, typed by whether the provider uses OAuth and
// sourced from the status label, else its source (interactive-mode.ts:5790-5796). It returns an untyped nil, not a nil pointer in the interface.
func authSelectorStatus(status ai.AuthStatus, usingOAuth bool) tui.AuthCheck {
	if !status.Configured {
		return nil
	}
	check := &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: status.Label}
	if usingOAuth {
		check.Type = ai.CredentialOAuth
	}
	if check.Source == "" {
		check.Source = string(status.Source)
	}
	return check
}
