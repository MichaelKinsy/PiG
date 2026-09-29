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
	"github.com/MichaelKinsy/PiG/internal/codingagent/llama"
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
	SwitchSession(ctx context.Context, path, cwdOverride string, projectTrustUI func(cwd string) extension.UIContext, options *extension.SwitchSessionOptions) (extension.CancelledResult, error)
	Fork(ctx context.Context, entryID string, options *extension.ForkOptions) (InteractiveForkResult, error)
	ImportFromJsonl(ctx context.Context, path, cwdOverride string) (extension.CancelledResult, error)
	// ExtensionCommandActions returns the command actions of session with new, fork and switch routed through the runtime.
	ExtensionCommandActions(session InteractiveSessionHandle) extension.CommandActions
	// EmitQuitShutdown emits session_shutdown for quit once; closing the runtime does not repeat it.
	EmitQuitShutdown()
	// SetBeforeSessionReplacement installs the drain that runs after a replacement is approved and before the outgoing Session aborts.
	SetBeforeSessionReplacement(drain func(context.Context) error)
	// SetBeforeSessionInvalidate installs the synchronous hook that runs after session_shutdown and before the outgoing Session's extension contexts go stale.
	SetBeforeSessionInvalidate(hook func())
	// SetRebindSession installs the awaited callback that runs once the replacement Session is installed.
	SetRebindSession(rebind func(ctx context.Context, session InteractiveSessionHandle) error)
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
	ExcludedTools           map[string]struct{}
	ToolRegistryAllowed     map[string]struct{}
	NoBuiltinTools          bool
	PromptPaths             []string
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
	ExtensionConflicts      []ExtensionConflict
	BuiltinExtensions       []extension.Extension
	ReloadBuiltinExtensions func() []extension.Extension
	Llama                   *llama.Host
	SubprocessUIBridge      SubprocessUIBridge
	SubprocessHost          SubprocessHost
	ModelLookup             func(providerID, modelID string) *ai.Model
	ModelCatalog            func() []*ai.Model
	RequestAuthRuntime      *RequestAuthRuntime
}

// installRuntimeHooks binds the runtime host to this mode as the constructor of Pi's InteractiveMode does: the mode resets extension UI before the outgoing Session's contexts go stale, drains active work before the outgoing Session aborts, and rebinds to the replacement Session.
func (m *InteractiveMode) installRuntimeHooks() {
	rt := m.opts.Runtime
	if rt == nil {
		return
	}
	rt.SetBeforeSessionReplacement(func(ctx context.Context) error {
		return m.runOnMainAndWait(ctx, m.settleActiveRun)
	})
	rt.SetBeforeSessionInvalidate(m.resetExtensionUIForReplacement)
	rt.SetRebindSession(m.rebindFromRuntime)
}

// resetExtensionUIForReplacement detaches what the outgoing Session's extensions put on screen before their contexts go stale.
func (m *InteractiveMode) resetExtensionUIForReplacement() {
	reset := func() {
		m.detachSubprocess()
		if m.widgetContainer != nil {
			m.syncWidgets(nil)
		}
		m.setRemoteEditor(nil)
		m.resetAutocompleteWrappers()
	}
	// The replacement runs on a worker while the owner services tasks. After the loop ended, nothing else touches the UI.
	if m.runCtx == nil || m.runEnded.Load() || m.runCtx.Err() != nil {
		reset()
		return
	}
	if err := m.runOnMainAndWait(m.runCtx, func() error { reset(); return nil }); err != nil {
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
	return m.rebindCurrentSession(ctx, true)
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
	m.opts.ExcludedTools = r.ExcludedTools
	m.opts.ToolRegistryAllowed = r.ToolRegistryAllowed
	m.opts.NoBuiltinTools = r.NoBuiltinTools
	m.opts.PromptPaths = r.PromptPaths
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
	event := r.SessionStartEvent
	m.opts.SessionStartEvent = &event
	m.opts.BuiltinExtensions = r.BuiltinExtensions
	m.opts.ReloadBuiltinExtensions = r.ReloadBuiltinExtensions
	m.opts.Llama = r.Llama
	m.opts.SubprocessUIBridge = r.SubprocessUIBridge
	m.opts.SubprocessHost = r.SubprocessHost
	m.opts.ModelLookup = r.ModelLookup
	m.opts.ModelCatalog = r.ModelCatalog
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
	m.loadPromptTemplates()
	if provider := m.opts.ResourceSourceInfoProvider; provider != nil {
		m.resourceSourceInfo = provider()
	}
	m.loadThemes()
	m.reloadIssues = nil
	m.extensionConflicts = r.ExtensionConflicts
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
		m.detachModelRegistry = m.wireSubprocessHostCallbacks()
	}
	if host := m.opts.SubprocessHost; host != nil {
		uiCtx := NewTUIUIContext(m.tuiInst)
		uiCtx.interactiveMode = m
		host.SetWidthFunc(func() int { return m.tuiInst.Width() })
		host.SetHeightFunc(func() int { return m.tuiInst.Height() })
		m.tuiInst.SetOnWidthChange(func(width int) { host.NotifyWidth(width) })
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
	if bridge := m.opts.SubprocessUIBridge; bridge != nil {
		bridge.SetUIContext(extension.NoopUIContext)
		bridge.SetWidgetSyncFunc(func(map[string]*subprocess.PushProxy) {})
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
	if rt := m.opts.Runtime; rt != nil {
		rt.EmitQuitShutdown()
		return
	}
	emitSessionShutdown(m.newRunner, "quit")
}
