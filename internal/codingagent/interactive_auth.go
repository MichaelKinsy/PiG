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
		all = m.withLlamaLoginProvider(all)
	}
	for i := range all {
		method := m.providerAuth(all[i].ID)
		if all[i].AuthType == "api_key" && method.APIKey != nil {
			all[i].MethodName = method.APIKey.Name
		}
		if all[i].AuthType == "oauth" && method.OAuth != nil {
			all[i].MethodName = method.OAuth.Name
			all[i].LoginLabel = method.OAuth.LoginLabel
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
		if !all[i].Stored {
			status := auth.GetAuthStatus(all[i].ID)
			status.Configured = status.Source != ""
			if m.opts.ModelRegistry != nil {
				status = m.opts.ModelRegistry.GetProviderAuthStatus(all[i].ID)
			}
			if status.Configured {
				all[i].AuthStatusSource = string(status.Source)
				all[i].AuthStatusLabel = status.Label
			}
		}
	}
	m.applyLlamaAuthStatus(all)
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

func (m *InteractiveMode) newLoginDialog(name string, cancel func(), titleOverride ...string) *tui.LoginDialog {
	dialog := tui.NewLoginDialog(name, cancel, titleOverride...)
	dialog.SetMaskSecretInput(m.maskSecretInput())
	return dialog
}

// runOAuthLogin runs the provider's interactive OAuth dialog.
func (m *InteractiveMode) runOAuthLogin(loginCtx context.Context, provider string) error {
	switch provider {
	case "github-copilot":
		return m.runLoginGitHubCopilotDialog(loginCtx)
	case "openai-codex":
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
	selector := tui.NewExtensionSelector(prompt.Message, labels)
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

func runOAuthProviderLogin(ctx context.Context, provider ai.OAuthProviderInterface, callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
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
		extension.CallInitiated(ctx)
		return "", fmt.Errorf("interactive selection is not available for %s; use a provider-specific login command", provider.Name())
	}

	cb := ai.OAuthLoginCallbacks{
		GetDeviceID:     m.loginDeviceID,
		OnPrompt:        func(value ai.OAuthPrompt) (string, error) { return prompt(context.Background(), value) },
		OnPromptContext: prompt,
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			showDeviceCode(dlg, info.VerificationURI, info.UserCode)
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

	authPath := auth.Path()
	var failed atomic.Bool
	go func() {
		defer loginCancel()

		cred, err := runOAuthProviderLogin(loginCtx, provider, cb)
		if err != nil {
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

	ok := m.runEditorSlotLoginDialog(dlg, renderNotify)
	if ok {
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
func loginDialogOutcome(dlg *tui.LoginDialog, failed *atomic.Bool, providerName string) error {
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
	methodSel := tui.NewExtensionSelector("Select OpenAI Codex login method:", []string{
		"Browser login (default)",
		"Device code login (headless)",
	})
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
			showDeviceCode(dlg, info.VerificationURI, info.UserCode)
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
			showDeviceCode(dlg, verificationURL, userCode)
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
	footer.SetRoutedModelSource(m.footerRoutedModel)
	footer.SetSubscriptionResolver(m.footerUsingSubscription)
	return footer
}

// routedModelHandle is implemented by a session handle whose session can route a virtual model selection.
type routedModelHandle interface {
	RoutedModelSelection() *RoutedModelSelection
}

// footerRoutedModel reads the physical model the current session's latest response was routed to (AgentSession.routedModel).
func (m *InteractiveMode) footerRoutedModel() *RoutedModelSelection {
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
func showDeviceCode(dlg *tui.LoginDialog, verificationURI, userCode string) {
	dlg.ShowDeviceCode(verificationURI, userCode)
	dlg.ShowWaiting("Waiting for authentication...")
}

// OpenBrowser opens target in the platform browser. The launch is best-effort, as upstream's: callers still present the target to the user, so a launcher failure is not reported.
// Ports packages/coding-agent/src/utils/open-browser.ts
func OpenBrowser(target string) { _ = openBrowser(target) }

// openBrowser opens a URL in the default browser without a shell.
// Ports packages/coding-agent/src/utils/open-browser.ts
// On Windows, cmd /c start would re-parse &, |, ^ in the URL, truncating OAuth URLs and running commands carried by a server-supplied device-code URI, so rundll32 receives the URL as one argument.
var openBrowser = func(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default: // linux, freebsd, etc.
		cmd = "xdg-open"
		args = []string{url}
	}
	command := exec.Command(cmd, args...)
	// Upstream's spawn finds the launcher with libuv's search on Windows.
	nodespawn.SetProgram(command)
	return command.Start()
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
