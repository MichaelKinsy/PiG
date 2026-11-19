package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts runtimeHost wiring: setBeforeSessionInvalidate, setRebindSession and rebindCurrentSession.

import (
	"context"
	"errors"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// InteractiveForkResult is the outcome of a runtime fork. SelectedText is absent when the fork is at an entry, and for a cancelled fork.
type InteractiveForkResult struct {
	Cancelled    bool
	SelectedText *string
}

// InteractiveRuntime is the Session replacement contract InteractiveMode needs from the runtime host that created its Session. *coding.Runtime satisfies it through the adapter in cmd/pig; the interface exists because internal/codingagent cannot import coding.
type InteractiveRuntime interface {
	NewSession(ctx context.Context, options *extension.NewSessionOptions) (extension.CancelledResult, error)
	// SwitchSession opens path and replaces the Session. projectTrustUI supplies the UI of the destination's project trust prompt, as Pi's projectTrustContextFactory does.
	SwitchSession(ctx context.Context, path, cwdOverride string, projectTrustUI func(cwd string) extension.ProjectTrustContext, options *extension.SwitchSessionOptions) (extension.CancelledResult, error)
	Fork(ctx context.Context, entryID string, options *extension.ForkOptions) (InteractiveForkResult, error)
	ImportFromJsonl(ctx context.Context, path, cwdOverride string) (extension.CancelledResult, error)
	// ExtensionCommandActions returns the command actions of session with new, fork and switch routed through the runtime.
	ExtensionCommandActions(session InteractiveSessionHandle) extension.CommandActions
	// EmitQuitShutdown emits session_shutdown for quit once; closing the runtime does not repeat it.
	EmitQuitShutdown()
	// EmitQuitShutdownAfterStop is EmitQuitShutdown for the quit by keys, which stops the TUI first: no input can arrive any more, and when nothing can make progress under the pending dispose the runtime ends the process, as Pi's process ends, and the call does not return.
	EmitQuitShutdownAfterStop()
	// SetBeforeQuitDispose installs the settle of the mode's active run, which EmitQuitShutdownAfterStop runs on the calling goroutine, the owner loop, before the dispose awaits the Session's abort.
	SetBeforeQuitDispose(settle func())
	// SetBeforeSessionReplacement installs the drain that runs after a replacement is approved and before the outgoing Session aborts.
	SetBeforeSessionReplacement(drain func(context.Context) error)
	// SetBeforeSessionInvalidate installs the synchronous hook that runs after session_shutdown and before the outgoing Session's extension contexts go stale.
	SetBeforeSessionInvalidate(hook func())
	// SetRebindSession installs the awaited callback that runs once the replacement Session is installed.
	SetRebindSession(rebind func(ctx context.Context, session InteractiveSessionHandle) error)
	// TakeUnreportedDiagnostics returns the current construction's diagnostics that were not reported yet and marks them reported (AgentSessionRuntime.takeUnreportedDiagnostics).
	TakeUnreportedDiagnostics() []AgentSessionRuntimeDiagnostic
}

// InteractiveReplacement carries the options of a replacement Session's cwd-bound build, as Pi reads them from the new Session's services. The mode applies it before it rebinds to the Session.
type InteractiveReplacement struct {
	CWD                     string
	SessionDir              string
	Settings                Settings
	SettingsManager         *SettingsManager
	ModelRegistry           *ModelRegistry
	SystemPrompt            string
	SystemPromptOptions     extension.BuildSystemPromptOptions
	AllowedTools            map[string]struct{}
	ActiveBuiltinTools      map[string]struct{}
	InitialActiveToolNames  []string
	ExcludedTools           map[string]struct{}
	ToolRegistryAllowed     map[string]struct{}
	NoBuiltinTools          bool
	PromptPaths             []string
	CLIPromptTemplatePaths  []string
	ThemePaths              []string
	SkillPaths              []string
	Skills                  []*SkillDef
	SkillDiagnostics        []extension.ResourceDiagnostic
	ContextFiles            []ContextFile
	SystemPromptSourcePaths []string
	RebuildSystemPrompt     func(skills []*SkillDef, contextFiles []ContextFile) (string, extension.BuildSystemPromptOptions)
	ResourceSourceInfo      func() map[string]ResourceSourceInfo
	ReloadResources         func() ReloadResourceSnapshot
	ExtensionRunner         *inproc.Runner
	SessionStartEvent       extension.SessionStartEvent
	// ExtensionConflicts are the tool and flag conflicts of the build's final extension set, which Pi lists with the extension load errors.
	ExtensionConflicts []ExtensionConflict
	// ExtensionWarnings are the install failures and package warnings of the build's load (resource-loader.ts getExtensions().warnings), listed with the extension load errors.
	ExtensionWarnings       []ExtensionWarning
	BuiltinExtensions       []extension.Extension
	ExplicitBuiltinPaths    []string
	ReloadBuiltinExtensions func() []extension.Extension
	SubprocessUIBridge      SubprocessUIBridge
	SubprocessHost          SubprocessHost
	// ModelBuilder is InteractiveOptions.ModelBuilder of the replacement Session's services: the selector lists from the replacement's registry (ModelRegistry, ModelCatalog), so it builds from it too.
	ModelBuilder        func(spec string) (*ai.Model, error)
	ModelBinder         func(*ai.Model) *ai.Model
	ModelLookup         func(providerID, modelID string) *ai.Model
	ModelCatalog        func(providerID ...string) []*ai.Model
	ModelClassify       func(context.Context, *ai.ClassifierModel, ai.ClassifierContext, ...ai.ModelsClassifierOptions) ai.ClassifierResult
	ModelGenerateImages func(context.Context, *ai.ImageModel, ai.ImagesContext, ...ai.ModelsImagesOptions) ai.AssistantImages
	RequestAuthRuntime  *RequestAuthRuntime
}

// installRuntimeHooks binds the runtime host to this mode as the constructor of Pi's InteractiveMode does: the mode resets extension UI before the outgoing Session's contexts go stale, drains active work before the outgoing Session aborts, and rebinds to the replacement Session.
func (m *InteractiveMode) installRuntimeHooks() {
	rt := m.runtimeHost
	if rt == nil {
		return
	}
	rt.SetBeforeSessionReplacement(func(ctx context.Context) error {
		return m.runOnMainAndWait(ctx, m.settleActiveRun)
	})
	rt.SetBeforeSessionInvalidate(m.resetExtensionUIForReplacement)
	rt.SetRebindSession(m.rebindFromRuntime)
	rt.SetBeforeQuitDispose(m.settleRunAtQuit)
}

// resetWorkingStatus returns the working message, visibility and indicator to their defaults, as Pi does when a Session replacement resets extension UI (interactive-mode.ts:2474-2482).
// Pi's reset words an already running working indicator "<default> (<interrupt key> to interrupt)", unlike the label a new indicator starts with.
func (m *InteractiveMode) resetWorkingStatus() {
	m.workingMessage = nil
	m.workingVisible = true
	m.setWorkingIndicator(nil)
	if indicator := m.activeStatusIndicator; indicator != nil && indicator.Kind == "working" {
		key := "escape"
		if m.keybindings != nil {
			key = m.keybindings.KeyText("app.interrupt")
		}
		indicator.SetMessage("Working (" + key + " to interrupt)")
		m.reconfigureRemoteEditor()
	}
}

// resetExtensionUI returns what extensions put on screen to the host's own, as Pi's resetExtensionUI does when a /reload begins and
// before a Session replacement invalidates the outgoing Session's extensions (interactive-mode.ts resetExtensionUI): their dialogs,
// overlays, terminal input listeners, footer, header, widgets, statuses, editor component and autocomplete providers go, and the
// terminal title, the working message, visibility and indicator and the hidden thinking label return to their defaults. The extensions
// that run afterwards set their own again; what an extension set before and does not set again is gone. The bridge forgets what it
// kept of the extension processes, which it would show a UI bound later. It runs on the owner loop.
func (m *InteractiveMode) resetExtensionUI() {
	m.resetExtensionDialogs()
	m.removeExtensionOverlays()
	m.clearExtensionTerminalInputListeners()
	// A slot or a label that no extension set is left alone: restoring what is already there would only draw the transcript again. The
	// header slot also shows the login an extension defined (D60), which a reload that fails keeps; a reload restores the built-in
	// header once the extensions have loaded.
	m.extComponentMu.Lock()
	footer, header := m.extFooterComponent != nil, m.extHeaderComponent != nil
	m.editorFactory = nil
	m.extComponentMu.Unlock()
	ui := &ExtUIContext{m: m}
	if footer {
		ui.SetFooter(nil)
	}
	if header {
		ui.SetHeader(nil)
	}
	if m.widgetContainer != nil {
		m.clearExtensionWidgets()
	}
	if m.statusLine != nil {
		m.statusLine.ClearExtensionStatuses()
	}
	m.setRemoteEditor(nil)
	m.resetAutocompleteWrappers()
	m.updateTerminalTitle()
	m.resetWorkingStatus()
	if m.hiddenThinkingLabel != nil {
		m.setHiddenThinkingLabel(nil)
	}
	if bridge := m.opts.SubprocessUIBridge; bridge != nil {
		bridge.ResetExtensionUI()
	}
}

// resetExtensionUIForReplacement detaches what the outgoing Session's extensions put on screen before their contexts go stale.
func (m *InteractiveMode) resetExtensionUIForReplacement() {
	reset := func() {
		m.resetExtensionUI()
		m.detachSubprocess()
		if m.widgetContainer != nil {
			m.syncWidgets(nil)
		}
	}
	// The replacement runs on a worker while the owner services tasks. After the loop ended, nothing else touches the UI.
	if m.runCtx == nil || m.runEnded.ended() || m.runCtx.Err() != nil {
		reset()
		return
	}
	// A signal's dispose holds shutdownMu while it waits for the owner loop; a quit that entered finishInteractiveShutdown meanwhile waits for that lock and serves nothing, so the wait ends and the reset runs here: the loop is parked and touches the UI no more.
	// A signal's dispose waits for the loop at most as long as it waits for extension handlers (shutdownExtensionsBriefly, the second it has always waited):
	// a loop stuck in a write to a terminal nobody reads would otherwise hold the signal for ever, and its title write would block as well, so it is skipped.
	var giveUp <-chan time.Time
	if m.signalDisposing.Load() {
		timer := time.NewTimer(m.signalDisposeBound())
		defer timer.Stop()
		giveUp = timer.C
	}
	switch err := m.runOnMainAndWaitUnless(m.runCtx, m.quitEnteredChan(), giveUp, func() error { reset(); return nil }); {
	case err == nil, errors.Is(err, errOwnerLoopBlocked):
	default:
		reset()
	}
}

// rebindFromRuntime installs the replacement Session's build on this mode and rebinds the mode to it, as setRebindSession's callback does upstream.
func (m *InteractiveMode) rebindFromRuntime(ctx context.Context, session InteractiveSessionHandle) error {
	if m.opts.ReplacementResources == nil {
		return errors.New("interactive: ReplacementResources is required when a Runtime replaces the Session")
	}
	replacement := m.opts.ReplacementResources(session)
	if err := m.runOnMainAndWait(ctx, func() error {
		m.applyReplacement(session, replacement)
		return nil
	}); err != nil {
		return err
	}
	if err := m.rebindCurrentSession(ctx, true); err != nil {
		return err
	}
	// interactive-mode.ts setRebindSession: the replacement's diagnostics follow the rebind and show in the chat before the command that replaced the Session reports its own result.
	var diagnostics []AgentSessionRuntimeDiagnostic
	if rt := m.runtimeHost; rt != nil {
		diagnostics = rt.TakeUnreportedDiagnostics()
	}
	return m.runOnMainAndWait(ctx, func() error {
		m.showRuntimeDiagnostics(diagnostics)
		return nil
	})
}

// applyReplacement moves the mode onto the replacement Session's build. It runs on the owner.
func (m *InteractiveMode) applyReplacement(session InteractiveSessionHandle, r InteractiveReplacement) {
	m.detachSubprocess()
	m.opts.SessionHandle = session
	m.opts.CWD = r.CWD
	m.opts.SessionDir = r.SessionDir
	m.opts.Settings = r.Settings
	m.opts.SettingsManager = r.SettingsManager
	m.opts.ModelRegistry = r.ModelRegistry
	m.turnSystemPromptMu.Lock()
	m.opts.SystemPrompt = r.SystemPrompt
	m.turnSystemPromptMu.Unlock()
	m.opts.SystemPromptOptions = r.SystemPromptOptions
	m.opts.AllowedTools = r.AllowedTools
	m.opts.ActiveBuiltinTools = r.ActiveBuiltinTools
	m.opts.InitialActiveToolNames = r.InitialActiveToolNames
	m.opts.ExcludedTools = r.ExcludedTools
	m.opts.ToolRegistryAllowed = r.ToolRegistryAllowed
	m.opts.NoBuiltinTools = r.NoBuiltinTools
	m.opts.PromptPaths = r.PromptPaths
	m.opts.CLIPromptTemplatePaths = r.CLIPromptTemplatePaths
	m.opts.ThemePaths = r.ThemePaths
	m.opts.SkillPaths = r.SkillPaths
	m.opts.Skills = r.Skills
	m.opts.SkillDiagnostics = r.SkillDiagnostics
	m.opts.ContextFiles = r.ContextFiles
	m.opts.SystemPromptSourcePaths = r.SystemPromptSourcePaths
	m.opts.RebuildSystemPrompt = r.RebuildSystemPrompt
	m.opts.ResourceSourceInfoProvider = r.ResourceSourceInfo
	m.opts.ReloadResourceProvider = r.ReloadResources
	m.opts.ExtensionRunner = r.ExtensionRunner
	m.newRunner = r.ExtensionRunner
	m.bindThinkingRuns()
	event := r.SessionStartEvent
	m.opts.SessionStartEvent = &event
	m.opts.BuiltinExtensions = r.BuiltinExtensions
	m.opts.ExplicitBuiltinPaths = r.ExplicitBuiltinPaths
	m.opts.ReloadBuiltinExtensions = r.ReloadBuiltinExtensions
	m.opts.SubprocessUIBridge = r.SubprocessUIBridge
	m.opts.SubprocessHost = r.SubprocessHost
	m.opts.ModelBuilder = r.ModelBuilder
	m.opts.ModelBinder = r.ModelBinder
	m.opts.ModelLookup = r.ModelLookup
	m.opts.ModelCatalog = r.ModelCatalog
	m.opts.ModelClassify = r.ModelClassify
	m.opts.ModelGenerateImages = r.ModelGenerateImages
	m.opts.RequestAuthRuntime = r.RequestAuthRuntime
	if m.extCtx != nil {
		m.extCtx.CWD = r.CWD
	}
	if event.Reason == "new" {
		m.restoreBuiltInHeader()
	}
	if r.SettingsManager != nil {
		tui.SetCapabilityOverrides(r.SettingsManager.GetTerminalCapabilityOverrides())
	}
	m.ensurePngTranscoder()
	m.loadPromptTemplates()
	m.reportMissingPromptTemplatePaths()
	if provider := m.opts.ResourceSourceInfoProvider; provider != nil {
		m.resourceSourceInfo = provider()
	}
	m.loadThemes()
	m.reloadIssues = nil
	m.extensionConflicts = r.ExtensionConflicts
	m.extensionWarnings = r.ExtensionWarnings
	m.publishSlashCommandCatalog()
	m.attachSubprocess()
	m.applyRuntimeSettings()
	m.rebuildToolSystemPrompt()
	m.initThinkingLevel()
	m.initScopedModels()
}

// attachSubprocess wires the current build's extension host and UI bridge to the live TUI.
func (m *InteractiveMode) attachSubprocess() {
	if m.tuiInst == nil {
		return
	}
	// The sprite catalogue holds exactly the bound build's extension sprites; the bridge replays its own below.
	if m.dropBoundSprites() {
		defer m.invalidateBuiltInHeader()
	}
	if bridge := m.opts.SubprocessUIBridge; bridge != nil {
		extUI := &ExtUIContext{m: m}
		if keybindings, ok := bridge.(interface{ SetKeybindingsFunc(func() any) }); ok {
			keybindings.SetKeybindingsFunc(func() any { return m.extensionKeybindings.Load() })
		}
		bridge.SetUIContext(extUI)
		bridge.SetInvalidate(m.requestRender)
		bridge.SetNotifyFunc(extUI.Notify)
		bridge.SetWidgetSyncFunc(func(widgets map[string]*subprocess.PushProxy) {
			m.syncWidgets(widgets)
		})
		// An extension process clearing a key a host-process extension set clears that widget: widget keys are one namespace.
		bridge.SetWidgetClearFunc(m.clearInprocessWidget)
		// A reload's new bridge learns that a frontend still draws.
		bridge.SetFrontend(m.surface != nil)
		m.detachModelRegistry = m.wireSubprocessHostCallbacks()
		m.attachTerminalViewClient(bridge)
		m.attachViewEndpoint(bridge)
	}
	if host := m.opts.SubprocessHost; host != nil {
		if resolved, ok := host.(interface{ SetToolRenderersResolvedFunc(func(string)) }); ok {
			resolved.SetToolRenderersResolvedFunc(func(toolName string) {
				m.runOnMain(m.runCtx, func() { m.reapplyToolPresentation(toolName) })
			})
		}
		uiCtx := NewTUIUIContext(m.tuiInst)
		uiCtx.interactiveMode = m
		host.SetWidthFunc(func() int { return m.tuiInst.Width() })
		host.SetHeightFunc(func() int { return m.tuiInst.Height() })
		m.tuiInst.SetOnWidthChange(func(width int) {
			host.NotifyWidth(width)
			m.viewClientResized()
		})
		if w := m.tuiInst.Width(); w > 0 {
			host.NotifyWidth(w)
		}
		if h := m.tuiInst.Height(); h > 0 {
			host.NotifyHeight(h)
		}
		host.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
			if host.IsShuttingDown() {
				return
			}
			level := "warning"
			if disabled {
				level = "error"
			}
			uiCtx.Notify(subprocess.FormatCrashNotice(name, delay, disabled, reason), level)
		})
	}
}

// detachSubprocess stops the outgoing build's extension host from reaching the TUI. A host whose command handler still runs stays alive until the handler returns, but it no longer draws.
func (m *InteractiveMode) detachSubprocess() {
	if m.detachModelRegistry != nil {
		m.detachModelRegistry()
		m.detachModelRegistry = nil
	}
	m.detachTerminalViewClient()
	m.detachViewEndpoint()
	if bridge := m.opts.SubprocessUIBridge; bridge != nil {
		bridge.SetUIContext(extension.NoopUIContext)
		bridge.SetWidgetSyncFunc(func(map[string]*subprocess.PushProxy) {})
		bridge.SetWidgetClearFunc(func(string) {})
		bridge.SetInvalidate(func() {})
		bridge.SetNotifyFunc(func(string, string) {})
	}
	if host := m.opts.SubprocessHost; host != nil {
		host.SetCrashHandler(func(string, time.Duration, bool, string) {})
	}
}

// runtimeImporter adapts the runtime host to the /import contract.
type runtimeImporter struct{ rt InteractiveRuntime }

func (i runtimeImporter) ImportFromJsonl(ctx context.Context, inputPath, cwdOverride string) (extension.CancelledResult, error) {
	return i.rt.ImportFromJsonl(ctx, inputPath, cwdOverride)
}

// emitQuitShutdown emits session_shutdown for quit through the runtime host when the mode has one, so closing the runtime does not repeat the event.
func (m *InteractiveMode) emitQuitShutdown() {
	if rt := m.runtimeHost; rt != nil {
		rt.EmitQuitShutdown()
		return
	}
	emitSessionShutdown(m.newRunner, "quit")
}

// emitQuitShutdownAfterStop is emitQuitShutdown for the quit by keys. Pi's shutdown() stops the TUI and then awaits runtimeHost.dispose() (interactive-mode.ts shutdown: drainInput, this.stop(), `await this.runtimeHost.dispose()`): stop() takes stdin out of the event loop, so a dispose that waits for an aborted run whose author never reacts, or for a session_shutdown handler that never settles, ends with the process (exit 0, no resume hint) once nothing else keeps the loop alive. The runtime host carries that end. The signal shutdown disposes before it stops the TUI and keeps the plain dispose.
func (m *InteractiveMode) emitQuitShutdownAfterStop() {
	if rt := m.runtimeHost; rt != nil {
		rt.EmitQuitShutdownAfterStop()
		return
	}
	emitSessionShutdown(m.newRunner, "quit")
}
