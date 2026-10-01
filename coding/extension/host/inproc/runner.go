package inproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// defaultStaleMessage matches the upstream default verbatim. This string
// is user-visible (surfaced in error reporting / diagnostics) and any
// drift would be a fidelity gap.
//
// upstream: runner.ts:461 (`invalidate(message = "...")`)
const defaultStaleMessage = "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload()."

// coerceResult attempts a direct type assertion of handlerResult to T.
// If that fails and handlerResult is json.RawMessage (the encoding returned
// by subprocess event handlers), it falls back to JSON unmarshal.
// Returns (result, true) on success, (zero, false) on failure.
//
// This bridges the subprocess extension protocol: which returns raw JSON
// from event handlers: with the inproc runner's typed Emit* methods that
// expect concrete Go structs.
func coerceResult[T any](handlerResult any) (T, bool) {
	if typed, ok := handlerResult.(T); ok {
		return typed, true
	}
	if raw, ok := handlerResult.(json.RawMessage); ok {
		var out T
		if err := json.Unmarshal(raw, &out); err == nil {
			return out, true
		}
	}
	if rawPtr, ok := handlerResult.(*json.RawMessage); ok && rawPtr != nil {
		var out T
		if err := json.Unmarshal(*rawPtr, &out); err == nil {
			return out, true
		}
	}
	var zero T
	return zero, false
}

// coerceInputEventResult handles the InputEventResult sealed interface,
// which cannot be generically unmarshalled. Falls back to
// extension.UnmarshalInputEventResult for json.RawMessage.
func coerceInputEventResult(handlerResult any) (extension.InputEventResult, bool) {
	if typed, ok := handlerResult.(extension.InputEventResult); ok {
		return typed, true
	}
	var raw json.RawMessage
	switch v := handlerResult.(type) {
	case json.RawMessage:
		raw = v
	case *json.RawMessage:
		if v != nil {
			raw = *v
		}
	}
	if len(raw) > 0 {
		result, err := extension.UnmarshalInputEventResult(raw)
		if err == nil {
			return result, true
		}
	}
	return nil, false
}

// StaleError carries the invalidation message alongside the canonical
// extension.ErrStaleContext sentinel. Use errors.Is for the sentinel
// check; errors.As for access to the message.
//
// The sentinel lives in the parent `coding/extension` package so every host
// package matches the same identity with errors.Is.
//
// upstream: runner.ts:467-471 (private assertActive throws Error(staleMessage))
type StaleError struct {
	Message string
}

func (e *StaleError) Error() string {
	if e.Message == "" {
		return defaultStaleMessage
	}
	return e.Message
}

// Is implements errors.Is so callers can match the canonical
// extension.ErrStaleContext sentinel.
func (e *StaleError) Is(target error) bool { return target == extension.ErrStaleContext }

// Runner is the in-process Go dispatch runner for extensions.
//
// upstream: runner.ts:219 (export class ExtensionRunner)
type Runner struct {
	// Field names mirror upstream private fields verbatim for sync
	// compatibility; see pig/AGENTS.md "Sync compatibility: the
	// prime directive".

	// extensions is the pre-populated state container slice, produced
	// by the loader. Order is load order; dispatch iterates in this
	// order.
	extensions []extension.Extension

	// cwd is the working directory captured at runner construction -
	// used by extension factories that resolve relative paths. Mirrors
	// upstream runner.ts:230.
	cwd string

	// staleMessage holds the invalidation reason as a *string; nil = active.
	//
	// pig translation rule (NOT a divergence: see docs/parity/DIVERGENCES.md
	// "TS→Go translation rituals"): atomic.Pointer for thread-safe
	// reads from any goroutine. Upstream runs single-threaded JS where
	// a plain mutable property suffices.
	staleMessage atomic.Pointer[string]

	// invalidateOnce guards first-invalidate-wins semantics. Upstream
	// achieves this with `if (!this.staleMessage)` (runner.ts:462); the
	// Go atomic equivalent under racing goroutines is sync.Once.
	invalidateOnce sync.Once

	// commandMu serializes command resolution and diagnostics. RPC clients may
	// query command inventory while another goroutine invokes a command.
	commandMu sync.Mutex

	// activeCommands counts command handlers that have not returned. A host that retires this Runner's extension process waits for it to reach zero so the handler that requested a Session replacement can still receive its result.
	activeCommandsMu   sync.Mutex
	activeCommands     int
	activeCommandsIdle sync.Cond

	// commandDiagnostics accumulates collision warnings from the most
	// recent Commands() call. Cleared at the start of every Commands().
	// Mirrors upstream `private commandDiagnostics: ResourceDiagnostic[]`
	// (runner.ts:243).
	commandDiagnostics []extension.ResourceDiagnostic

	// shortcutDiagnostics accumulates collision warnings from the most
	// recent Shortcuts() call. Cleared at the start of every Shortcuts().
	// Mirrors upstream `private shortcutDiagnostics: ResourceDiagnostic[]`
	// (runner.ts:242).
	shortcutDiagnostics []extension.ResourceDiagnostic

	// errorListenersMu guards errorListeners against concurrent
	// AddErrorListener calls (and concurrent unsubscribe calls). The
	// emit-side iteration takes the read lock so listeners can be
	// fired concurrently with subscription mutations.
	//
	// pig translation rule (NOT a divergence): upstream uses a
	// `Set<ErrorListener>` mutated single-threaded; Go needs an
	// explicit lock because handler dispatch and AddErrorListener can
	// race when called from different goroutines.
	errorListenersMu sync.RWMutex

	// errorListeners is the slice of registered error-listener
	// callbacks. Slice (not map) so iteration order is deterministic
	// (registration order); listeners can register and unregister
	// without affecting other listeners' identities. Mirrors upstream
	// `private errorListeners: Set<ErrorListener> = new Set()`
	// (runner.ts:469).
	errorListeners []*extension.ErrorListener

	// uiContext is the per-mode UI surface (interactive TUI, RPC,
	// print). Default [extension.NoopUIContext] until
	// [Runner.SetUIContext] binds a real one.
	//
	// upstream: runner.ts:225 (`private uiContext: ExtensionUIContext`)
	uiContext extension.UIContext
	uiMu      sync.RWMutex
	mode      extension.ExtensionMode

	// uiPrompts tracks blocking extension UI prompts for ui_prompt_start
	// and ui_prompt_end. See ui_prompt.go.
	//
	// upstream: runner.ts uiPromptDepth / activeUIPrompt
	uiPrompts uiPromptTracker

	// contextActions holds the host-injected context-action callbacks
	// (Model, IsIdle, Shutdown, etc.) that back the corresponding
	// surfaces on extension.Context. Wired via BindCore.
	//
	// Default value (zero ContextActions{}) yields upstream-equivalent
	// no-op defaults via Context's nil-checks (true/false/nil/no-op).
	//
	// upstream: runner.ts:227-241 (private isIdleFn / hasPendingMessagesFn /
	// shutdownHandler / getContextUsageFn / compactFn / getModel /
	// getSystemPromptFn fields default to stubs).
	contextActions extension.ContextActions

	// sessionManager is the log ctx.sessionManager exposes. It is bound apart from contextActions so a Session that swaps its log in place (coding.Session.ReplaceInner) can rebind it while handlers run; see [Runner.BindSessionManager] and [Runner.actions].
	sessionManager atomic.Pointer[extension.SessionManager]

	// extActions retains the bound host actions; runtime shares provider registration state with the loader.
	extActions extension.ExtensionActions
	runtime    *extension.ExtensionRuntime

	// commandActions holds command-specific callbacks (WaitForIdle,
	// NewSession, Fork, NavigateTree, SwitchSession, Reload) that
	// back [extension.CommandContext] surfaces. Wired via
	// [Runner.BindCommandActions].
	//
	// upstream: runner.ts:628-660 (createCommandContext consumes these)
	commandActions extension.CommandActions
	flagMu         sync.RWMutex
	flagValues     map[string]any

	// mcp delivers mcp_servers_change events and remembers which servers were reported as unhandled. See mcp_servers.go.
	mcp mcpServersState
	// background is cancelled when the runner is invalidated, so events queued for delivery end with it.
	background       context.Context
	cancelBackground context.CancelFunc
}

// Runtime returns the registration state the runner shares with its loader: MCP servers, providers and virtual models.
func (r *Runner) Runtime() *extension.ExtensionRuntime { return r.runtime }

// NewRunner constructs a Runner over the given pre-populated extensions. Supply the loader's shared runtime to bind its pending provider registrations; otherwise the runner owns a fresh runtime.
//
// extensions is typically produced by a loader (compiled-in registration
// or the subprocess transport adapter). cwd is captured for relative path
// resolution by extension factories.
//
// upstream: runner.ts:233-242 (constructor)
func NewRunner(extensions []extension.Extension, cwd string, sharedRuntime ...*extension.ExtensionRuntime) *Runner {
	runtime := extension.CreateExtensionRuntime()
	if len(sharedRuntime) != 0 && sharedRuntime[0] != nil {
		runtime = sharedRuntime[0]
	}
	flagValues := make(map[string]any)
	for _, ext := range extensions {
		for name, flag := range ext.Flags {
			if _, exists := flagValues[name]; !exists && flag.Default != nil {
				flagValues[name] = flag.Default
			}
		}
	}
	for i := range extensions {
		ext := &extensions[i]
		ext.InitializeEventHandlers()
		if ext.SourceInfo == nil {
			ext.SourceInfo = defaultExtensionSourceInfo(*ext, cwd)
		}
		if len(ext.CommandOrder) == 0 && len(ext.Commands) == 1 {
			for name := range ext.Commands {
				ext.CommandOrder = append(ext.CommandOrder, name)
			}
		}
		for name, command := range ext.Commands {
			if command.SourceInfo == nil {
				command.SourceInfo = ext.SourceInfo
				ext.Commands[name] = command
			}
		}
	}
	r := &Runner{
		extensions: extensions,
		runtime:    runtime,
		cwd:        cwd,
		uiContext:  extension.NoopUIContext,
		flagValues: flagValues,
	}
	r.background, r.cancelBackground = context.WithCancel(context.Background())
	r.activeCommandsIdle.L = &r.activeCommandsMu
	r.contextActions.GetFlagValue = r.flagValue
	r.contextActions.GetUIContext = r.GetUIContext
	r.contextActions.GetMode = r.getMode
	return r
}

func defaultExtensionSourceInfo(ext extension.Extension, cwd string) extension.SourceInfo {
	path := extensionPath(ext)
	if path == "" {
		path = ext.Name
	}
	source := "local"
	baseDir := ""
	if strings.HasPrefix(path, "builtin:") {
		source = "builtin"
	} else if path != "" {
		if filepath.IsAbs(path) {
			baseDir = filepath.Dir(path)
		} else if cwd != "" {
			baseDir = filepath.Dir(filepath.Join(cwd, path))
		}
	}
	info := map[string]any{
		"path": path, "source": source, "scope": "temporary", "origin": "top-level",
	}
	if baseDir != "" {
		info["baseDir"] = baseDir
	}
	return info
}

// ExtensionPaths returns the resolved paths of all loaded extensions, in
// load order. Used for diagnostics, "/extensions" listing, and provenance
// tracking.
//
// upstream: runner.ts:146-149 (getExtensionPaths)
func (r *Runner) ExtensionPaths() []string {
	paths := make([]string, 0, len(r.extensions))
	for _, ext := range r.extensions {
		paths = append(paths, ext.ResolvedPath)
	}
	return paths
}

// ExtensionSource is one loaded extension's resolved path and SourceInfo. Hidden extensions are omitted from the
// startup Extensions list (upstream extension.hidden, types.ts:2213).
type ExtensionSource struct {
	ResolvedPath string
	SourceInfo   extension.SourceInfo
	Hidden       bool
}

// ExtensionSources returns the resolved path and SourceInfo of every loaded
// extension in load order, which the interactive loaded-resources listing
// labels and groups as upstream does.
func (r *Runner) ExtensionSources() []ExtensionSource {
	sources := make([]ExtensionSource, 0, len(r.extensions))
	for _, ext := range r.extensions {
		sources = append(sources, ExtensionSource{ResolvedPath: ext.ResolvedPath, SourceInfo: ext.SourceInfo, Hidden: ext.Hidden})
	}
	return sources
}

// ExtensionNames returns human-readable extension names in load order.
func (r *Runner) ExtensionNames() []string {
	names := make([]string, 0, len(r.extensions))
	for _, ext := range r.extensions {
		name := ext.Name
		if name == "" {
			name = ext.Path
		}
		if name == "" {
			name = ext.ResolvedPath
		}
		names = append(names, name)
	}
	return names
}

// ExtensionCount returns the number of loaded extensions.
// SDK-surface accessor: a Go read-only helper so callers get the count without
// exposing the internal extensions slice. Upstream TS callers reach into runner
// state freely; this is a language-surface addition over the same wire protocol,
// not a behavioral divergence.
func (r *Runner) ExtensionCount() int { return len(r.extensions) }

// ─── Stale lifecycle ──────────────────────────────────────────────────────

// Invalidate marks the runner as stale. After this returns, IsStale()
// reports true and assertActive() (via every API method that checks it)
// returns a *StaleError matching ErrStaleContext. Emit and EmitBoundary still
// deliver events, as upstream emit and emitBoundary do; the ctx getters and
// actions a handler uses report the stale runner.
//
// First-call-wins: subsequent Invalidate calls with different messages
// are no-ops. This mirrors upstream runner.ts:461-466 (`if (!this.staleMessage)`).
//
// Pass message="" to use the upstream default; pass a custom message to
// surface a specific reason in error reporting.
//
// upstream: runner.ts:461-466 (invalidate)
func (r *Runner) Invalidate(message string) {
	r.invalidateOnce.Do(func() {
		if message == "" {
			message = defaultStaleMessage
		}
		r.staleMessage.Store(&message)
		r.cancelBackground()
		r.uiPrompts.mu.Lock()
		if r.uiPrompts.cancel != nil {
			r.uiPrompts.cancel()
		}
		r.uiPrompts.pending = nil
		r.uiPrompts.mu.Unlock()
	})
}

// Shutdown triggers graceful shutdown via the host-installed shutdown
// handler. No-op if no handler is wired (matches upstream default at
// runner.ts:345: `this.shutdownHandler = async () => {};`).
//
// upstream: runner.ts:558 (`shutdown(): void { this.shutdownHandler(); }`)
func (r *Runner) Shutdown() {
	if r.contextActions.Shutdown != nil {
		r.contextActions.Shutdown()
	}
}

// BindCore installs the host-injected callbacks that back the
// dynamic surfaces on extension.Context (Model, IsIdle, Shutdown,
// etc.) plus the agent-loop and provider action sets. It reports queued provider failures through error listeners without stopping the drain; subsequent registration calls use the registry immediately.
//
// Idempotent in spirit but NOT thread-safe relative to in-flight
// dispatches: the host must call this during runtime construction,
// before the runner is exposed to handlers. Per upstream's bindCore
// invariant: "called once during runtime initialization".
//
// upstream: runner.ts:265-294
func (r *Runner) BindCore(
	actions extension.ExtensionActions,
	contextActions extension.ContextActions,
	providerActions *extension.ProviderActions,
) {
	r.extActions = actions
	if contextActions.GetScopedModels == nil {
		contextActions.GetScopedModels = r.contextActions.GetScopedModels
	}
	// upstream: runner.ts:362, 398-405, 889-891 (ctx.sessionManager is a constructor field no binding replaces); agent-session.ts:3173-3194, 3301 (a mode's bindExtensions reaches bindCore only through _bindExtensionCore, which binds executeTool, getCallableTools and appendEntry). A mode binding that omits them keeps the Session's.
	if contextActions.ExecuteTool == nil {
		contextActions.ExecuteTool = r.contextActions.ExecuteTool
	}
	if contextActions.GetCallableTools == nil {
		contextActions.GetCallableTools = r.contextActions.GetCallableTools
	}
	if contextActions.AppendEntry == nil {
		contextActions.AppendEntry = r.contextActions.AppendEntry
	}
	// upstream: agent-session.ts:3353 (_bindExtensionCore binds refreshTools); a mode binding that omits it keeps the Session's.
	if contextActions.RefreshTools == nil {
		contextActions.RefreshTools = r.contextActions.RefreshTools
	}
	r.contextActions = contextActions
	r.contextActions.SessionManager = nil
	if contextActions.SessionManager != nil {
		r.BindSessionManager(contextActions.SessionManager)
	}
	r.contextActions.GetUIContext = r.GetUIContext
	r.contextActions.GetMode = r.getMode
	lookup := contextActions.GetFlagValue
	r.contextActions.GetFlagValue = func(name string) any {
		if lookup != nil {
			if value := lookup(name); value != nil {
				return value
			}
		}
		return r.flagValue(name)
	}
	// Upstream's sendUserMessage lives on ExtensionAPI, which every handler
	// receives. Pig delivers it through the per-extension Context, so the
	// handler-facing bundle carries the same action the command path uses.
	r.contextActions.SendUserMessage = actions.SendUserMessage
	var providers extension.ProviderActions
	if registry, ok := contextActions.ModelRegistry.(providerRegistry); ok {
		providers.RegisterProvider = registry.RegisterProvider
		providers.UnregisterProvider = registry.UnregisterProvider
	}
	// upstream: runner.ts:497-500, 538-541: a provider action wins over the model registry's own virtual-model methods.
	if registry, ok := contextActions.ModelRegistry.(virtualModelRegistry); ok {
		providers.RegisterVirtualModel = registry.RegisterVirtualModel
		providers.UnregisterVirtualModel = registry.UnregisterVirtualModel
	}
	if providerActions != nil {
		if providerActions.RegisterProvider != nil {
			providers.RegisterProvider = providerActions.RegisterProvider
		}
		if providerActions.UnregisterProvider != nil {
			providers.UnregisterProvider = providerActions.UnregisterProvider
		}
		if providerActions.RegisterVirtualModel != nil {
			providers.RegisterVirtualModel = providerActions.RegisterVirtualModel
		}
		if providerActions.UnregisterVirtualModel != nil {
			providers.UnregisterVirtualModel = providerActions.UnregisterVirtualModel
		}
	}
	// upstream: runner.ts:436 (runtime.createContext = () => this.createContext()); runner.ts:457-462 (change listener).
	r.runtime.SetContextFactory(func() (*extension.Context, error) {
		return extension.NewContext(r.cwd, nil, r.assertActive, r.actions()), nil
	})
	r.runtime.SetMcpServersChangeListener(r.onMcpServersChange)
	r.runtime.BindProviderActions(providers, r.emitError)
}

type providerRegistry interface {
	RegisterProvider(string, extension.ProviderConfig) error
	UnregisterProvider(string)
}

type virtualModelRegistry interface {
	RegisterVirtualModel(extension.VirtualModelDefinition) error
	UnregisterVirtualModel(provider, id string)
}

// BindSessionManager rebinds the log ctx.sessionManager exposes to contexts made afterwards. It is safe to call while handlers run.
//
// upstream: runner.ts:889-891 (`get sessionManager()` reads the runner's session manager); a replacement binds a new AgentSession's manager to a new runner (agent-session-runtime.ts), which Go's in-place Session.ReplaceInner has no counterpart for.
func (r *Runner) BindSessionManager(manager extension.SessionManager) {
	r.sessionManager.Store(&manager)
}

// actions is the context actions a new context captures: the bound set with the live session manager.
func (r *Runner) actions() extension.ContextActions {
	actions := r.contextActions
	// upstream: loader.ts:475-478 (getMcpServers reads the runtime the loader shares with the runner).
	actions.GetMcpServers = r.runtime.McpServers
	if manager := r.sessionManager.Load(); manager != nil {
		actions.SessionManager = *manager
	}
	return actions
}

// BindAbort installs the active Session's non-blocking abort action without replacing the mode's other context actions. Call it before dispatching handlers.
// upstream: packages/coding-agent/src/core/agent-session.ts:_bindExtensionCore
func (r *Runner) BindAbort(abort func()) {
	r.contextActions.Abort = abort
}

// BindSystemPromptOptions installs a live Session options source when the mode has not supplied one. Bind before handler dispatch.
func (r *Runner) BindSystemPromptOptions(get func() *extension.BuildSystemPromptOptions) {
	if r.contextActions.GetSystemPromptOptions == nil {
		r.contextActions.GetSystemPromptOptions = get
	}
}

// BindTools installs the Session's live tool/state views and message delivery action without replacing unrelated mode callbacks. Call it before dispatching handlers.
// upstream: packages/coding-agent/src/core/agent-session.ts:_bindExtensionCore
func (r *Runner) BindTools(actions extension.ContextActions) {
	r.contextActions.GetAllTools = actions.GetAllTools
	r.contextActions.GetActiveTools = actions.GetActiveTools
	r.contextActions.SetActiveTools = actions.SetActiveTools
	if actions.RefreshTools != nil {
		r.contextActions.RefreshTools = actions.RefreshTools
	}
	r.contextActions.GetSystemPrompt = actions.GetSystemPrompt
	if actions.GetModel != nil {
		r.contextActions.GetModel = actions.GetModel
	}
	if actions.SessionManager != nil {
		r.BindSessionManager(actions.SessionManager)
	}
	if actions.ModelRegistry != nil {
		r.contextActions.ModelRegistry = actions.ModelRegistry
	}
	if actions.IsIdle != nil {
		r.contextActions.IsIdle = actions.IsIdle
	}
	if actions.GetSignal != nil {
		r.contextActions.GetSignal = actions.GetSignal
	}
	if actions.HasPendingMessages != nil {
		r.contextActions.HasPendingMessages = actions.HasPendingMessages
	}
	if actions.SendUserMessage != nil {
		r.contextActions.SendUserMessage = actions.SendUserMessage
	}
	// upstream: agent-session.ts:_bindExtensionCore passes executeTool and getCallableTools with the other context actions.
	if actions.ExecuteTool != nil {
		r.contextActions.ExecuteTool = actions.ExecuteTool
	}
	if actions.GetCallableTools != nil {
		r.contextActions.GetCallableTools = actions.GetCallableTools
	}
	if actions.AppendEntry != nil {
		r.contextActions.AppendEntry = actions.AppendEntry
	}
}

// BindCommandActions installs the command-specific actions used by
// [Runner.CreateCommandContext]. Each non-nil action replaces the bound one
// and a nil action keeps it, so the Session's own actions (bound in every
// mode) and a mode's actions compose whichever binds first.
//
// upstream: runner.ts:628 (createCommandContext consumes runner.* fields
// set during bindExtensions)
func (r *Runner) BindCommandActions(actions extension.CommandActions) {
	bound := &r.commandActions
	if actions.WaitForIdle != nil || actions.WaitForIdleContext != nil {
		bound.WaitForIdle, bound.WaitForIdleContext = actions.WaitForIdle, actions.WaitForIdleContext
	}
	if actions.NewSession != nil || actions.NewSessionContext != nil {
		bound.NewSession, bound.NewSessionContext = actions.NewSession, actions.NewSessionContext
	}
	if actions.Fork != nil || actions.ForkContext != nil {
		bound.Fork, bound.ForkContext = actions.Fork, actions.ForkContext
	}
	if actions.NavigateTree != nil || actions.NavigateTreeContext != nil {
		bound.NavigateTree, bound.NavigateTreeContext = actions.NavigateTree, actions.NavigateTreeContext
	}
	if actions.SwitchSession != nil || actions.SwitchSessionContext != nil {
		bound.SwitchSession, bound.SwitchSessionContext = actions.SwitchSession, actions.SwitchSessionContext
	}
	if actions.Reload != nil || actions.ReloadContext != nil {
		bound.Reload, bound.ReloadContext = actions.Reload, actions.ReloadContext
	}
}

// CreateCommandContext builds an [extension.CommandContext] for
// dispatching an extension-registered slash command. The returned
// context embeds the base Context (CWD + all ContextActions surfaces)
// plus the command-specific actions (WaitForIdle, Fork, etc.).
//
// upstream: runner.ts:628-660 (createCommandContext)
func (r *Runner) CreateCommandContext() *extension.CommandContext {
	base := extension.NewContext(r.cwd, nil, r.assertActive, r.actions())
	return extension.NewCommandContext(base, r.commandActions)
}

// SetUIContext replaces the UI binding and mode. An omitted mode means print. Nil UI means no UI, independently of the selected mode. Existing contexts read the new binding. Dialogs on a supplied UI report prompt start and end through this runner.
// upstream: packages/coding-agent/src/core/extensions/runner.ts:setUIContext
func (r *Runner) SetUIContext(uiContext extension.UIContext, mode ...extension.ExtensionMode) {
	selected := extension.ModePrint
	if len(mode) != 0 {
		selected = mode[0]
	}
	ui := extension.NoopUIContext
	if uiContext != nil {
		ui = &uiPromptContext{UIContext: uiContext, runner: r}
	}
	r.uiMu.Lock()
	r.uiContext = ui
	r.mode = selected
	r.uiMu.Unlock()
}

// GetUIContext returns the currently bound UI surface. Always
// non-nil; defaults to [extension.NoopUIContext].
//
// upstream: runner.ts:356-358
func (r *Runner) GetUIContext() extension.UIContext {
	r.uiMu.RLock()
	defer r.uiMu.RUnlock()
	return r.uiContext
}

func (r *Runner) getMode() extension.ExtensionMode {
	r.uiMu.RLock()
	defer r.uiMu.RUnlock()
	return r.mode
}

// HasUI reports whether a non-noop UI context is bound. Mirrors
// upstream's pointer-identity check verbatim (runner.ts:361 -
// `this.uiContext !== noOpUIContext`).
//
// upstream: runner.ts:359-362
func (r *Runner) HasUI() bool {
	return r.GetUIContext() != extension.NoopUIContext
}

// IsStale reports whether Invalidate has been called.
//
// SDK-surface accessor: upstream has no public introspection method: TS callers
// detect staleness only by catching the throw from a guarded API call. This Go
// read-only helper lets hosts check status without an emit call (e.g.
// `coding/Session` lifecycle code asking "is this runner still mine?"). A
// language-surface addition over the same wire protocol, not a behavioral
// divergence.
//
// upstream: derived from the staleMessage check in runner.ts:468.
func (r *Runner) IsStale() bool { return r.staleMessage.Load() != nil }

// StaleMessage returns the invalidation message, or "" if the runner is
// active.
//
// SDK-surface accessor: see IsStale.
func (r *Runner) StaleMessage() string {
	if msg := r.staleMessage.Load(); msg != nil {
		return *msg
	}
	return ""
}

// assertActive returns a *StaleError if Invalidate has been called.
// Every API method that mutates or reads runner state should call this
// first.
//
// upstream: runner.ts:467-471 (private assertActive)
func (r *Runner) assertActive() error {
	if msg := r.staleMessage.Load(); msg != nil {
		return &StaleError{Message: *msg}
	}
	return nil
}

// Tools returns the tool definitions registered across all extensions,
// deduplicated by tool name with first-wins semantics: when two extensions
// register a tool with the same name, the earlier-loaded extension's
// registration is kept and the later one is silently skipped.
//
// upstream: runner.ts:369-379 (getAllRegisteredTools)
func (r *Runner) Tools() []extension.RegisteredTool {
	seen := make(map[string]struct{})
	var out []extension.RegisteredTool
	for _, ext := range r.extensions {
		for _, tool := range ext.RegisteredTools() {
			name := tool.Definition.Name
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, tool)
		}
	}
	return out
}

// ToolSourceInfo returns the SourceInfo of the extension whose tool
// [Runner.Tools] reports under toolName. Upstream registerTool stamps the
// registering extension's sourceInfo onto the tool (loader.ts), and
// getAllTools reports it. [extension.RegisteredTool.SourceInfo] carries
// PiG's per-tool source attribution for Piglet scoping instead (D23).
//
// upstream: runner.ts getAllRegisteredTools (RegisteredTool.sourceInfo)
func (r *Runner) ToolSourceInfo(toolName string) (extension.SourceInfo, bool) {
	for _, ext := range r.extensions {
		if _, ok := ext.RegisteredTool(toolName); ok {
			return ext.SourceInfo, true
		}
	}
	return nil, false
}

// GetToolDefinition returns the definition for the named tool. The
// boolean reports whether the tool was found. First-wins by name across
// extensions (mirrors the dedup rule in [Runner.Tools]).
//
// upstream: runner.ts:382-390 (getToolDefinition)
func (r *Runner) GetToolDefinition(toolName string) (extension.ToolDefinition, bool) {
	for _, ext := range r.extensions {
		if tool, ok := ext.RegisteredTool(toolName); ok {
			return tool.Definition, true
		}
	}
	return extension.ToolDefinition{}, false
}

// Commands returns the resolved slash commands across all extensions,
// with first-wins suffix-disambiguated invocation names. Two commands
// with the same name `foo` produce invocation names `foo:1` and `foo:2`
// (occurrence order = load order); a unique name keeps the bare form.
//
// Side effect: clears the command diagnostics buffer (mirrors upstream
// `getRegisteredCommands` runner.ts:541-544 which assigns
// `this.commandDiagnostics = []` before running resolveRegisteredCommands).
//
// upstream: runner.ts:505-544 (resolveRegisteredCommands + getRegisteredCommands)
func (r *Runner) Commands() []extension.ResolvedCommand {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	r.commandDiagnostics = nil
	return r.resolveCommands()
}

// Command returns the resolved command whose invocation name matches.
// The boolean reports whether it was found.
//
// upstream: runner.ts:550-552 (getCommand). Upstream re-resolves on
// every call: so does this. No caching.
func (r *Runner) Command(invocationName string) (extension.ResolvedCommand, bool) {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	for _, c := range r.resolveCommands() {
		if c.InvocationName == invocationName {
			return c, true
		}
	}
	return extension.ResolvedCommand{}, false
}

// ExecuteCommand resolves and invokes an extension slash command through the
// active runner and its command context. Handler errors are reported through
// the runner error channel and still count as handled, matching pinned Pi.
func (r *Runner) ExecuteCommand(ctx context.Context, invocationName, args string) bool {
	command, ok := r.Command(invocationName)
	if !ok {
		return false
	}
	if command.Handler == nil {
		return true
	}
	commandContext := r.CreateCommandContext()
	callContext := extension.WithContext(ctx, commandContext.Context)
	callContext = extension.WithCommandContext(callContext, commandContext)
	r.beginCommand()
	defer r.endCommand()
	if err := command.Handler(callContext, args); err != nil {
		r.recordHandlerError(ctx, "command:"+invocationName, "command", err)
	}
	return true
}

func (r *Runner) beginCommand() {
	r.activeCommandsMu.Lock()
	r.activeCommands++
	r.activeCommandsMu.Unlock()
}

func (r *Runner) endCommand() {
	r.activeCommandsMu.Lock()
	r.activeCommands--
	if r.activeCommands == 0 {
		r.activeCommandsIdle.Broadcast()
	}
	r.activeCommandsMu.Unlock()
}

// ActiveCommands reports how many command handlers are running.
func (r *Runner) ActiveCommands() int {
	r.activeCommandsMu.Lock()
	defer r.activeCommandsMu.Unlock()
	return r.activeCommands
}

// WaitForCommands blocks until no command handler is running or ctx ends. A handler that awaits a Session replacement is still running while the replacement retires this Runner, so callers wait from a goroutine they own.
func (r *Runner) WaitForCommands(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() {
		r.activeCommandsMu.Lock()
		r.activeCommandsIdle.Broadcast()
		r.activeCommandsMu.Unlock()
	})
	defer stop()
	r.activeCommandsMu.Lock()
	defer r.activeCommandsMu.Unlock()
	for r.activeCommands > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.activeCommandsIdle.Wait()
	}
	return nil
}

// CommandDiagnostics returns the warnings/errors collected during the
// most recent Commands() call. Empty until Commands() runs at least
// once.
//
// upstream: runner.ts:546-548 (getCommandDiagnostics)
func (r *Runner) CommandDiagnostics() []extension.ResourceDiagnostic {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	return append([]extension.ResourceDiagnostic(nil), r.commandDiagnostics...)
}

// resolveCommands walks every extension's Commands map, gathers the
// raw RegisteredCommand list, then assigns disambiguated invocation
// names (mirrors upstream resolveRegisteredCommands runner.ts:505-539
// verbatim).
//
// Algorithm (faithful port):
//
//  1. Pass 1: walk all extensions, accumulate commands and per-name counts.
//  2. Pass 2: for each command, occurrence = (seen[name] += 1).
//     - If the name has count > 1 (collides): try invocationName = "name:occurrence".
//     - Else: try the bare name.
//     - If the candidate is already taken (rare: happens if name itself
//     looks like "foo:1" pre-collision), increment suffix until free.
func (r *Runner) resolveCommands() []extension.ResolvedCommand {
	var commands []extension.RegisteredCommand
	counts := map[string]int{}
	for _, ext := range r.extensions {
		seenNames := make(map[string]struct{}, len(ext.Commands))
		for _, name := range ext.CommandOrder {
			cmd, ok := ext.Commands[name]
			if !ok {
				continue
			}
			commands = append(commands, cmd)
			counts[cmd.Name]++
			seenNames[name] = struct{}{}
		}
		// Legacy direct Extension values may not carry CommandOrder. Preserve
		// their existing map behavior without inventing a secondary sort.
		for name, cmd := range ext.Commands {
			if _, ordered := seenNames[name]; ordered {
				continue
			}
			commands = append(commands, cmd)
			counts[cmd.Name]++
		}
	}

	seen := map[string]int{}
	taken := map[string]struct{}{}
	out := make([]extension.ResolvedCommand, 0, len(commands))
	for _, cmd := range commands {
		seen[cmd.Name]++
		occurrence := seen[cmd.Name]

		var invocationName string
		if counts[cmd.Name] > 1 {
			invocationName = fmt.Sprintf("%s:%d", cmd.Name, occurrence)
		} else {
			invocationName = cmd.Name
		}
		// Pathological case: candidate already taken (name itself was
		// "foo:1" pre-collision). Walk the suffix until free.
		suffix := occurrence
		for _, dup := taken[invocationName]; dup; _, dup = taken[invocationName] {
			suffix++
			invocationName = fmt.Sprintf("%s:%d", cmd.Name, suffix)
		}
		taken[invocationName] = struct{}{}

		out = append(out, extension.ResolvedCommand{
			RegisteredCommand: cmd,
			InvocationName:    invocationName,
		})
	}
	return out
}

// Flags returns declarations from the first extension registering each name.
// upstream: packages/coding-agent/src/core/extensions/runner.ts:getFlags
func (r *Runner) Flags() map[string]extension.ExtensionFlag {
	out := map[string]extension.ExtensionFlag{}
	for _, ext := range r.extensions {
		for name, flag := range ext.Flags {
			if _, dup := out[name]; dup {
				continue
			}
			out[name] = flag
		}
	}
	return out
}

// SetFlagValue stores a runtime CLI override. Flag values are booleans or strings.
// Ports packages/coding-agent/src/core/extensions/runner.ts
// upstream: packages/coding-agent/src/core/extensions/runner.ts:setFlagValue
func (r *Runner) SetFlagValue(name string, value any) {
	r.flagMu.Lock()
	defer r.flagMu.Unlock()
	r.flagValues[name] = value
}

// GetFlagValues returns a copy of the runtime flags, including registered defaults.
// upstream: packages/coding-agent/src/core/extensions/runner.ts:getFlagValues
func (r *Runner) GetFlagValues() map[string]any {
	r.flagMu.RLock()
	defer r.flagMu.RUnlock()
	return maps.Clone(r.flagValues)
}

func (r *Runner) flagValue(name string) any {
	r.flagMu.RLock()
	defer r.flagMu.RUnlock()
	return r.flagValues[name]
}

// HasHandlers reports whether any extension declares a non-empty handler
// list for the given event type. Used by the host to skip emit calls for
// events nobody listens to (perf), and by test/diagnostics to introspect
// runner state.
//
// upstream: runner.ts:485-493 (hasHandlers)
func (r *Runner) HasHandlers(eventType string) bool {
	for _, ext := range r.extensions {
		if handlers := ext.EventHandlers(eventType); len(handlers) > 0 {
			return true
		}
	}
	return false
}

// Shortcuts returns all extension shortcuts after applying built-in conflicts.
// Reserved editor-global bindings reject an extension shortcut. Other built-in
// conflicts warn and allow the extension. Extension-to-extension conflicts use
// normalized last-wins semantics.
type builtInKeyBinding struct {
	keybinding       string
	restrictOverride bool
}

var reservedKeybindings = map[string]struct{}{
	"app.interrupt": {}, "app.clear": {}, "app.exit": {}, "app.suspend": {},
	"app.thinking.cycle": {}, "app.model.cycleForward": {}, "app.model.cycleBackward": {},
	"app.model.select": {}, "app.tools.expand": {}, "app.thinking.toggle": {},
	"app.editor.external": {}, "app.message.copy": {}, "app.message.followUp": {},
	"tui.input.submit": {}, "tui.select.confirm": {}, "tui.select.cancel": {},
	"tui.input.copy": {}, "tui.editor.deleteToLineEnd": {},
}

func (r *Runner) Shortcuts(resolvedKeybindings map[string][]string) map[extension.KeyID]extension.ExtensionShortcut {
	r.shortcutDiagnostics = nil
	builtinKeybindings := map[extension.KeyID]builtInKeyBinding{}
	if resolvedKeybindings != nil {
		for _, action := range slices.Sorted(maps.Keys(resolvedKeybindings)) {
			_, restricted := reservedKeybindings[action]
			for _, key := range resolvedKeybindings[action] {
				normalized := extension.KeyID(strings.ToLower(key))
				existing, exists := builtinKeybindings[normalized]
				if exists && existing.restrictOverride && !restricted {
					continue
				}
				builtinKeybindings[normalized] = builtInKeyBinding{keybinding: action, restrictOverride: restricted}
			}
		}
	}
	out := map[extension.KeyID]extension.ExtensionShortcut{}
	for _, ext := range r.extensions {
		for key, shortcut := range ext.Shortcuts {
			normalized := extension.KeyID(strings.ToLower(string(key)))
			if builtin, ok := builtinKeybindings[normalized]; ok {
				if builtin.restrictOverride {
					r.shortcutDiagnostics = append(r.shortcutDiagnostics, extension.ResourceDiagnostic{
						Type:    extension.DiagnosticWarning,
						Message: fmt.Sprintf("Extension shortcut '%s' from %s conflicts with built-in shortcut. Skipping.", key, shortcut.ExtensionPath),
						Path:    shortcut.ExtensionPath,
					})
					continue
				}
				r.shortcutDiagnostics = append(r.shortcutDiagnostics, extension.ResourceDiagnostic{
					Type:    extension.DiagnosticWarning,
					Message: fmt.Sprintf("Extension shortcut conflict: '%s' is built-in shortcut for %s and %s. Using %s.", key, builtin.keybinding, shortcut.ExtensionPath, shortcut.ExtensionPath),
					Path:    shortcut.ExtensionPath,
				})
			}
			if _, duplicate := out[normalized]; duplicate {
				r.shortcutDiagnostics = append(r.shortcutDiagnostics, extension.ResourceDiagnostic{
					Type:    extension.DiagnosticWarning,
					Message: fmt.Sprintf("Extension shortcut conflict: '%s' registered by multiple extensions. Using %s.", key, shortcut.ExtensionPath),
					Path:    shortcut.ExtensionPath,
				})
			}
			out[normalized] = shortcut
		}
	}
	return out
}

// ShortcutDiagnostics returns the warnings accumulated during the most
// recent Shortcuts() call. Empty until Shortcuts() runs at least once.
//
// upstream: runner.ts:457-459 (getShortcutDiagnostics)
func (r *Runner) ShortcutDiagnostics() []extension.ResourceDiagnostic {
	return r.shortcutDiagnostics
}

// MessageRenderer returns the renderer for the given custom message type
// if any extension registered one. First match across extensions wins
// (load order).
//
// upstream: runner.ts:495-503 (getMessageRenderer)
func (r *Runner) MessageRenderer(customType string) extension.MessageRenderer {
	for _, ext := range r.extensions {
		if renderer, ok := ext.MessageRenderers[customType]; ok {
			return renderer
		}
	}
	return nil
}

// GetMarkdownTransformers returns the Markdown transformers extensions
// registered, in extension load order.
//
// upstream: runner.ts getMarkdownTransformers
func (r *Runner) GetMarkdownTransformers() []extension.MarkdownTransformer {
	var transformers []extension.MarkdownTransformer
	for _, ext := range r.extensions {
		if ext.MarkdownTransformer != nil {
			transformers = append(transformers, ext.MarkdownTransformer)
		}
	}
	return transformers
}

// EntryRenderer returns the renderer for the given custom entry type if any
// extension registered one. First match across extensions wins (load order).
//
// upstream: runner.ts (getEntryRenderer)
func (r *Runner) EntryRenderer(customType string) extension.EntryRenderer {
	for _, ext := range r.extensions {
		if renderer, ok := ext.EntryRenderers[customType]; ok {
			return renderer
		}
	}
	return nil
}

// AddErrorListener registers a synchronous callback. Each call creates an
// independent registration. The returned function removes only that registration.
func (r *Runner) AddErrorListener(listener extension.ErrorListener) func() {
	if listener == nil {
		return func() {}
	}

	ref := &listener
	r.errorListenersMu.Lock()
	r.errorListeners = append(r.errorListeners, ref)
	r.errorListenersMu.Unlock()

	var removed atomic.Bool
	return func() {
		if removed.Swap(true) {
			return
		}
		r.errorListenersMu.Lock()
		defer r.errorListenersMu.Unlock()
		for i, l := range r.errorListeners {
			if l == ref {
				r.errorListeners = append(r.errorListeners[:i], r.errorListeners[i+1:]...)
				return
			}
		}
	}
}

// EmitError reports err to the error listeners. Hosts use it for failures
// they detect around extension dispatch. Mirrors upstream runner.emitError,
// which agent-session calls for boundary, command, and skill failures.
func (r *Runner) EmitError(err *extension.ExtensionError) { r.emitError(err) }

// emitError calls a stable listener snapshot synchronously.
func (r *Runner) emitError(err *extension.ExtensionError) {
	if err == nil {
		return
	}
	r.errorListenersMu.RLock()
	listeners := make([]*extension.ErrorListener, len(r.errorListeners))
	copy(listeners, r.errorListeners)
	r.errorListenersMu.RUnlock()

	for _, listener := range listeners {
		(*listener)(err)
	}
}

// DispatchContext builds a context.Context enriched with the runner's
// extension.Context. Used by the command bridge (interactive.go) to
// call new-style command handlers from the legacy slash dispatch path.
//
// upstream: runner.ts:566-630 (createContext factory, invoked per-event)
func (r *Runner) DispatchContext(ctx context.Context) context.Context {
	return r.dispatchContext(ctx)
}

// dispatchContext returns a context.Context with the per-extension
// extension.Context attached. The Context carries the runner's CWD and
// assertActive guard so that every getter on the Context rejects calls
// on a stale runner.
//
// upstream: runner.ts:673 (const ctx = this.createContext())
//
// The attached Context exposes the actions bound to this Runner. Unbound
// optional actions retain their documented zero-value behavior.
func (r *Runner) dispatchContext(ctx context.Context) context.Context {
	extCtx := extension.NewContext(r.cwd, nil, r.assertActive, r.actions())
	return extension.WithContext(ctx, extCtx)
}

type eventHandlerSnapshot struct {
	ext      *extension.Extension
	handlers []extension.HandlerFn
}

// snapshotEventHandlers captures every extension before the first callback runs. Registration changes affect only a subsequent or nested dispatch.
// upstream: packages/coding-agent/src/core/extensions/runner.ts:snapshotEventHandlers
func snapshotEventHandlers(extensions []extension.Extension, event string) []eventHandlerSnapshot {
	snapshots := make([]eventHandlerSnapshot, 0, len(extensions))
	for i := range extensions {
		ext := &extensions[i]
		if handlers := ext.EventHandlers(event); len(handlers) != 0 {
			snapshots = append(snapshots, eventHandlerSnapshot{ext: ext, handlers: handlers})
		}
	}
	return snapshots
}

// EmitProjectTrust dispatches project_trust in extension and registration
// order. Undecided handlers fall through; the first decisive result wins.
// Handler failures are collected and do not stop later handlers, matching the
// pre-runtime upstream helper.
func EmitProjectTrust(r *Runner, ctx context.Context, event extension.ProjectTrustEvent) (*extension.ProjectTrustEventResult, []extension.ExtensionError, error) {
	if err := r.assertActive(); err != nil {
		return nil, nil, err
	}
	dispatchCtx := r.dispatchContext(ctx)
	errors := make([]extension.ExtensionError, 0)
	for _, snapshot := range snapshotEventHandlers(r.extensions, "project_trust") {
		ext := snapshot.ext
		for _, handler := range snapshot.handlers {
			result, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				errors = append(errors, extension.ExtensionError{
					ExtensionPath: extensionPath(*ext),
					Event:         "project_trust",
					Error:         err.Error(),
					Stack:         extension.ErrorStack(err),
				})
				continue
			}
			if result == nil {
				errors = append(errors, extension.ExtensionError{
					ExtensionPath: extensionPath(*ext),
					Event:         "project_trust",
					Error:         "project_trust handler returned no result",
				})
				continue
			}
			coerced, ok := coerceResult[extension.ProjectTrustEventResult](result)
			if !ok {
				errors = append(errors, extension.ExtensionError{
					ExtensionPath: extensionPath(*ext),
					Event:         "project_trust",
					Error:         fmt.Sprintf("project_trust handler returned %T", result),
				})
				continue
			}
			if coerced.Trusted == extension.ProjectTrustUndecided {
				continue
			}
			return &coerced, errors, nil
		}
	}
	return nil, errors, nil
}

// extensionPath is the path ExtensionError reports: upstream uses ext.path.
func extensionPath(ext extension.Extension) string {
	if ext.Path != "" {
		return ext.Path
	}
	return ext.ResolvedPath
}

// recordHandlerError wraps a handler-returned error into ExtensionError
// and dispatches it via emitError. The stack is the failure's own, as
// upstream's `err.stack` (see [extension.ErrorStack]); the host's dispatch
// stack says nothing about the extension and is never reported.
//
// A call the host cut short is not reported: the error is the dispatch
// context's own cancellation, or the transport marks it
// [extension.ErrHandlerStopped] because the host stopped the extension.
// Failures whose diagnostic the subprocess lifecycle handler owns are not reported either.
func (r *Runner) recordHandlerError(ctx context.Context, extPath, eventType string, err error) {
	if err == nil || errors.Is(err, extension.ErrHandlerStopped) || (ctx.Err() != nil && errors.Is(err, ctx.Err())) {
		return
	}
	// pig divergence (D56): a failed subprocess connection has one lifecycle diagnostic, not another notification for every interrupted handler.
	if _, owned := errors.AsType[*invocation.LifecycleError](err); owned {
		return
	}
	stack := ""
	// upstream: packages/coding-agent/src/core/agent-session.ts:_tryExecuteExtensionCommand omits the stack from command errors.
	if eventType != "command" {
		stack = extension.ErrorStack(err)
	}
	r.emitError(&extension.ExtensionError{
		ExtensionPath: extPath,
		Event:         eventType,
		Error:         err.Error(),
		Stack:         stack,
	})
}

// BoundaryDispatchResult is the chained, validated result of turn_end or
// agent_before_settle handlers.
type BoundaryDispatchResult struct {
	Entries  []extension.SessionBoundaryDraft
	Continue bool
	Context  extension.BoundaryContextPreview
	Valid    bool
}

// EmitBoundary awaits boundary handlers in extension and registration order.
// Each handler observes the previous proposal and a freshly projected preview;
// invalid drafts are reported and may be repaired by a later handler.
func (r *Runner) EmitBoundary(
	ctx context.Context,
	baseEvent extension.BoundaryBaseEvent,
	buildContext func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error),
) (BoundaryDispatchResult, error) {
	// Upstream emitBoundary delivers after invalidation; only the ctx getters
	// and actions a handler uses report the stale runner (runner.ts:809-888).
	eventType, outcome, err := boundaryBaseFields(baseEvent)
	if err != nil {
		return BoundaryDispatchResult{}, err
	}
	entries := []extension.SessionBoundaryDraft{}
	shouldContinue := false
	preview, err := buildContext(entries)
	if err != nil {
		return BoundaryDispatchResult{}, err
	}
	valid := true
	dispatchCtx := r.dispatchContext(ctx)
	for _, snapshot := range snapshotEventHandlers(r.extensions, eventType) {
		ext := snapshot.ext
		for _, handler := range snapshot.handlers {
			event, state := withBoundaryState(baseEvent, extension.BoundaryState{Entries: entries, Continue: shouldContinue, Context: preview, Outcome: outcome})
			handlerResult, handlerErr := callHandler(handler, event, dispatchCtx)
			entries = state.Entries
			if handlerErr != nil {
				r.recordHandlerError(ctx, ext.Path, eventType, handlerErr)
			} else if handlerResult != nil {
				result, ok := coerceResult[extension.BoundaryResult](handlerResult)
				if !ok {
					if ptr, ptrOK := coerceResult[*extension.BoundaryResult](handlerResult); ptrOK && ptr != nil {
						result, ok = *ptr, true
					}
				}
				if !ok {
					r.recordHandlerError(ctx, ext.Path, eventType, fmt.Errorf("handler returned %T, expected extension.BoundaryResult", handlerResult))
				} else {
					if result.Entries != nil {
						entries = *result.Entries
					}
					if result.Continue != nil {
						shouldContinue = *result.Continue
					}
				}
			}
			projected, projectErr := buildContext(entries)
			if projectErr != nil {
				valid = false
				r.emitError(&extension.ExtensionError{
					ExtensionPath: extensionPath(*ext),
					Event:         eventType,
					Error:         "Invalid boundary entries: " + projectErr.Error(),
				})
				continue
			}
			preview = projected
			valid = true
		}
	}
	if !valid {
		return BoundaryDispatchResult{Context: preview}, nil
	}
	return BoundaryDispatchResult{Entries: entries, Continue: shouldContinue, Context: preview, Valid: true}, nil
}

func boundaryBaseFields(event extension.BoundaryBaseEvent) (string, extension.AgentActivityOutcome, error) {
	switch event := event.(type) {
	case extension.TurnEndEvent:
		if event.BoundaryState != nil {
			return event.Type, event.Outcome, nil
		}
	case *extension.AgentBeforeSettleEvent:
		if event != nil {
			return event.Type, event.Outcome, nil
		}
	}
	return "", "", errors.New("boundary event has no activity outcome")
}

func withBoundaryState(base extension.BoundaryBaseEvent, state extension.BoundaryState) (any, *extension.BoundaryState) {
	switch event := base.(type) {
	case extension.TurnEndEvent:
		event.BoundaryState = &state
		return event, &state
	case *extension.AgentBeforeSettleEvent:
		current := &extension.AgentBeforeSettleEvent{Type: event.Type, BoundaryState: state}
		return current, &current.BoundaryState
	}
	panic("invalid boundary event")
}

// EmitToolCall dispatches a ToolCallEvent to each handler in load order.
// It returns the first blocking result or the last non-blocking result.
// Handler errors stop dispatch and return to the caller, as in upstream.
func (r *Runner) EmitToolCall(ctx context.Context, event extension.ToolCallEvent) (*extension.ToolCallEventResult, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}
	if event == nil {
		return nil, nil
	}

	dispatchCtx := r.dispatchContext(ctx)
	var result *extension.ToolCallEventResult

	for _, snapshot := range snapshotEventHandlers(r.extensions, "tool_call") {
		ext, handlers := snapshot.ext, snapshot.handlers
		if len(handlers) == 0 {
			continue
		}
		for _, handler := range handlers {
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				return nil, err
			}
			if handlerResult == nil {
				continue
			}
			typed, ok := coerceResult[*extension.ToolCallEventResult](handlerResult)
			if !ok {
				r.recordHandlerError(ctx, ext.Path, "tool_call",
					fmt.Errorf("handler returned %T, expected *extension.ToolCallEventResult", handlerResult))
				continue
			}
			result = typed
			if typed.Block {
				return typed, nil
			}
		}
	}
	return result, nil
}

// EmitToolResult dispatches a ToolResultEvent to every "tool_result"
// handler across all extensions, in load order. Each handler can mutate
// `content` / `details` / `isError`; mutations chain through subsequent
// handlers (the next handler sees the previous handler's output, not
// the original input). Returns the combined modification, or nil if no
// handler modified anything.
//
// Returns (nil, *StaleError) if the runner has been invalidated.
//
// upstream: runner.ts:707-755 (emitToolResult)
//
// pig-specific: handler errors route via emitError (matches upstream's
// explicit try/catch in this method, runner.ts:733-740).
func (r *Runner) EmitToolResult(ctx context.Context, event extension.ToolResultEvent) (*extension.ToolResultEventResult, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}
	if event == nil {
		return nil, nil
	}

	dispatchCtx := r.dispatchContext(ctx)

	// Upstream spreads the event into currentEvent and writes each handler's
	// content/details/isError/usage into it, so every handler sees its
	// predecessors' values. The chained values are rebuilt into the event's
	// own variant before each handler.
	curContent, curDetails, curIsError, curUsage, curStructured := extractToolResultFields(event)
	modified := false

	for _, snapshot := range snapshotEventHandlers(r.extensions, "tool_result") {
		ext, handlers := snapshot.ext, snapshot.handlers
		if len(handlers) == 0 {
			continue
		}
		for _, handler := range handlers {
			event = withToolResultFields(event, curContent, curDetails, curIsError, curUsage, curStructured)
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "tool_result", err)
				continue
			}
			if handlerResult == nil {
				continue
			}
			typed, ok := coerceResult[*extension.ToolResultEventResult](handlerResult)
			if !ok {
				r.recordHandlerError(ctx, ext.Path, "tool_result",
					fmt.Errorf("handler returned %T, expected *extension.ToolResultEventResult", handlerResult))
				continue
			}

			if typed.Content != nil {
				curContent = typed.Content
				// upstream: structured content that is not replaced along with the content may no longer match it (runner.ts:1187).
				if typed.StructuredContent == nil {
					curStructured = nil
				}
				modified = true
			}
			if typed.Details != nil {
				curDetails = typed.Details
				modified = true
			}
			// upstream: `if (handlerResult.isError !== undefined)`. An omitted
			// isError keeps the chained flag.
			if typed.IsError != nil {
				curIsError = *typed.IsError
				modified = true
			}
			if typed.StructuredContent != nil {
				curStructured = typed.StructuredContent
				modified = true
			}
			if typed.Usage != nil {
				curUsage = typed.Usage
				modified = true
			}
		}
	}

	if !modified {
		return nil, nil
	}
	return &extension.ToolResultEventResult{
		Content:           curContent,
		Details:           curDetails,
		StructuredContent: curStructured,
		IsError:           &curIsError,
		Usage:             curUsage,
	}, nil
}

// EmitInput dispatches an input event to every "input" handler across
// all extensions, in load order. streamingBehavior identifies a prompt queued
// as a steer or follow-up; an empty value represents idle input. The dispatch
// is a chain-transform with **handled-short-circuit** semantics:
//
//   - InputEventResultContinue   no-op (next handler sees same text/images)
//   - InputEventResultTransform  text/images update for next handler
//   - InputEventResultHandled    halts dispatch, surfaces "input was consumed"
//
// Return contract:
//   - If any handler returned Handled, that result is returned (chain stops).
//   - Otherwise, if any handler transformed text or images, returns a
//     Transform result with the final chained values.
//   - Otherwise returns Continue.
//
// Returns (nil, *StaleError) if the runner has been invalidated.
//
// Handler errors are routed via emitError and the chain continues
// (matches upstream's explicit try/catch at runner.ts:1004-1011).
//
// upstream: runner.ts:1196-1228 (emitInput)
func (r *Runner) EmitInput(ctx context.Context, text string, images []extension.ImageContent, source extension.InputSource, streamingBehavior string) (extension.InputEventResult, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}

	dispatchCtx := r.dispatchContext(ctx)
	currentText := text
	currentImages := images
	imagesChanged := false // pig: explicit flag instead of upstream's reference-equality.

	for _, snapshot := range snapshotEventHandlers(r.extensions, "input") {
		ext, handlers := snapshot.ext, snapshot.handlers
		if len(handlers) == 0 {
			continue
		}
		for _, handler := range handlers {
			event := extension.InputEvent{
				Type:              "input",
				Text:              currentText,
				Images:            currentImages,
				Source:            source,
				StreamingBehavior: streamingBehavior,
			}
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "input", err)
				continue
			}
			if handlerResult == nil {
				continue
			}
			typed, ok := coerceInputEventResult(handlerResult)
			if !ok {
				r.recordHandlerError(ctx, ext.Path, "input",
					fmt.Errorf("handler returned %T, expected extension.InputEventResult", handlerResult))
				continue
			}

			switch v := typed.(type) {
			case extension.InputEventResultHandled:
				return v, nil
			case extension.InputEventResultTransform:
				currentText = v.Text
				// upstream: `result.images ?? currentImages` -
				// nil/undefined Images means "keep current".
				if v.Images != nil {
					currentImages = v.Images
					imagesChanged = true
				}
			case extension.InputEventResultContinue:
				// no-op
			}
		}
	}

	if currentText != text || imagesChanged {
		return extension.InputEventResultTransform{
			Text:   currentText,
			Images: currentImages,
		}, nil
	}
	return extension.InputEventResultContinue{}, nil
}

// EmitContext threads a messages slice through every "context"
// handler. Each handler can return a ContextEventResult with a new
// `Messages` slice; subsequent handlers see the previous handler's
// output. The runner returns the final chained slice.
//
// Upstream uses `structuredClone(messages)` to defensively deep-copy the
// input. Pig preserves the concrete Go values while recursively copying their
// pointers, interfaces, structs, slices, and maps.
//
// Returns (nil, *StaleError) if the runner has been invalidated.
//
// Handler errors route via emitError; chain continues.
//
// upstream: runner.ts:809-839 (emitContext)
func cloneAgentMessages(messages []extension.AgentMessage) []extension.AgentMessage {
	cloned := make([]extension.AgentMessage, len(messages))
	for i, message := range messages {
		value := cloneMessageValue(reflect.ValueOf(message))
		if value.IsValid() {
			cloned[i] = value.Interface()
		}
	}
	return cloned
}

func cloneMessageValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := cloneMessageValue(value.Elem())
		out := reflect.New(value.Type()).Elem()
		out.Set(cloned)
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(cloneMessageValue(value.Elem()))
		return out
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			out.SetMapIndex(cloneMessageValue(iter.Key()), cloneMessageValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := range value.Len() {
			out.Index(i).Set(cloneMessageValue(value.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(value.Type()).Elem()
		for i := range value.Len() {
			out.Index(i).Set(cloneMessageValue(value.Index(i)))
		}
		return out
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		out.Set(value)
		for i := range value.NumField() {
			if out.Field(i).CanSet() && value.Field(i).CanInterface() {
				out.Field(i).Set(cloneMessageValue(value.Field(i)))
			}
		}
		return out
	default:
		return value
	}
}

func (r *Runner) EmitContext(ctx context.Context, messages []extension.AgentMessage) ([]extension.AgentMessage, error) {
	out, _, err := r.EmitContextTracked(ctx, messages)
	return out, err
}

// EmitContextTracked is EmitContext that also reports whether any handler
// replaced the conversation list. Upstream compares list and message
// identity after each handler: an in-place edit, or a returned list holding
// the same message objects in the same order, leaves the conversation
// "unchanged", so the caller keeps every system message where it was
// (restoreSystemMessages). Only a replacement collapses them into one leading
// system message.
//
// upstream: runner.ts emitContext, sameMessages, restoreSystemMessages
func (r *Runner) EmitContextTracked(ctx context.Context, messages []extension.AgentMessage) ([]extension.AgentMessage, bool, error) {
	if err := r.assertActive(); err != nil {
		return nil, false, err
	}

	dispatchCtx := r.dispatchContext(ctx)
	current := cloneAgentMessages(messages)
	replaced := false

	for _, snapshot := range snapshotEventHandlers(r.extensions, "context") {
		ext, handlers := snapshot.ext, snapshot.handlers
		if len(handlers) == 0 {
			continue
		}
		for _, handler := range handlers {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			// Handlers see their own list: an in-place reorder is a change to
			// that list (compared with the snapshot below), and a handler that
			// fails loses its list changes, as upstream's filtered
			// visibleMessages array does. Field edits reach the shared messages.
			snapshot := current
			given := slices.Clone(current)
			event := extension.ContextEvent{Type: "context", Messages: given}
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, false, contextErr
			}
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "context", err)
				continue
			}
			var returned []extension.AgentMessage
			if handlerResult != nil {
				typed, ok := coerceResult[*extension.ContextEventResult](handlerResult)
				if !ok {
					r.recordHandlerError(ctx, ext.Path, "context",
						fmt.Errorf("handler returned %T, expected *extension.ContextEventResult", handlerResult))
					continue
				}
				returned = typed.Messages
			}
			if returned != nil {
				// handlerResult.messages
				if !contextListUnchanged(handlerResult, returned, snapshot) {
					replaced = true
				}
				current = returned
				continue
			}
			// No returned list: upstream uses event.messages when the handler
			// changed that list in place.
			if !sameMessageList(given, snapshot) {
				replaced = true
				current = given
			}
		}
	}
	return current, replaced, nil
}

// contextListUnchanged reports whether a context handler's returned list is
// the list it was given, as upstream sameMessages decides by identity. A
// subprocess SDK decides identity on its side of the wire and reports it as
// _pigContextUnchanged; a missing report counts as a replacement.
func contextListUnchanged(handlerResult any, returned, given []extension.AgentMessage) bool {
	if len(returned) != len(given) {
		return false
	}
	var raw []byte
	switch value := handlerResult.(type) {
	case json.RawMessage:
		raw = value
	case *json.RawMessage:
		if value != nil {
			raw = *value
		}
	}
	if raw != nil {
		var report struct {
			Unchanged *bool `json:"_pigContextUnchanged"`
		}
		if json.Unmarshal(raw, &report) == nil && report.Unchanged != nil {
			return *report.Unchanged
		}
		// An SDK without an identity report (Rust) keeps value equality.
		return sameMessageValues(returned, given)
	}
	return sameMessageList(returned, given)
}

// sameMessageList is upstream sameMessages: same length, same message
// identities in the same order.
func sameMessageList(left, right []extension.AgentMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !sameMessageIdentity(reflect.ValueOf(left[i]), reflect.ValueOf(right[i])) {
			return false
		}
	}
	return true
}

// sameMessageValues compares messages by their JSON value, for transports
// that cannot report identity.
func sameMessageValues(left, right []extension.AgentMessage) bool {
	if len(left) != len(right) {
		return false
	}
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	if errA != nil || errB != nil {
		return false
	}
	var av, bv any
	return json.Unmarshal(a, &av) == nil && json.Unmarshal(b, &bv) == nil && reflect.DeepEqual(av, bv)
}

// sameMessageIdentity is JavaScript object identity for Go message values:
// references (pointers, maps, slices) must be the same reference, and a
// struct is the same message when every field is. agent.AgentMessage holds
// its message by pointer, so an in-place edit keeps identity while a rebuilt
// message does not.
func sameMessageIdentity(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Type() != b.Type() {
		return false
	}
	switch a.Kind() {
	case reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return sameMessageIdentity(a.Elem(), b.Elem())
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return a.Pointer() == b.Pointer() && (a.Kind() != reflect.Slice || a.Len() == b.Len())
	case reflect.Struct:
		for i := range a.NumField() {
			if !sameMessageIdentity(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Array:
		for i := range a.Len() {
			if !sameMessageIdentity(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	default:
		// The remaining kinds are scalars, which compare by value.
		return a.Equal(b)
	}
}

// EmitContextWithSystem runs the second request-time phase: every
// "context_with_system" handler sees the full transcript, system messages
// included, and its returned messages are used as returned. Removing the
// leading system message is reported but honored. Handler errors are reported
// and the chain continues.
//
// upstream: runner.ts emitContext, context_with_system phase
func (r *Runner) EmitContextWithSystem(ctx context.Context, messages []extension.AgentMessage) ([]extension.AgentMessage, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}
	dispatchCtx := r.dispatchContext(ctx)
	// Handlers may edit messages in place; the transform is request-only
	// (upstream structuredClone before both phases), so they get a copy.
	current := cloneAgentMessages(messages)
	for _, snapshot := range snapshotEventHandlers(r.extensions, "context_with_system") {
		ext := snapshot.ext
		for _, handler := range snapshot.handlers {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			hadLeadingSystem := len(current) > 0 && messageRole(current[0]) == "system"
			event := extension.ContextWithSystemEvent{Type: "context_with_system", Messages: current}
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "context_with_system", err)
				continue
			}
			if handlerResult != nil {
				typed, ok := coerceResult[*extension.ContextEventResult](handlerResult)
				if !ok {
					r.recordHandlerError(ctx, ext.Path, "context_with_system",
						fmt.Errorf("handler returned %T, expected *extension.ContextEventResult", handlerResult))
					continue
				}
				if typed.Messages != nil {
					current = typed.Messages
				}
			}
			if hadLeadingSystem && (len(current) == 0 || messageRole(current[0]) != "system") {
				r.recordHandlerError(ctx, ext.Path, "context_with_system", errLeadingSystemRemoved)
			}
		}
	}
	return current, nil
}

var errLeadingSystemRemoved = errors.New("Handler removed the leading system message; the request has no prompt or initial tool declarations. Keep it at index 0 or replace a dropped prefix with getCurrentSystemMessage().")

// EmitBeforeProviderRequest threads a provider-request payload through
// every "before_provider_request" handler. Each handler can return a
// new payload; subsequent handlers see the previous handler's output.
// The runner returns the final chained payload.
//
// Returns (nil, *StaleError) if the runner has been invalidated.
//
// Handler errors route via emitError; chain continues.
//
// upstream: runner.ts:841-873 (emitBeforeProviderRequest)
//
// pig note: the payload type is `any` because upstream types it as
// `unknown` (provider-specific). Callers cast back to the concrete
// provider request type after this returns.
func (r *Runner) EmitBeforeProviderRequest(ctx context.Context, payload any) (any, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}

	dispatchCtx := r.dispatchContext(ctx)
	current := payload

	for _, snapshot := range snapshotEventHandlers(r.extensions, "before_provider_request") {
		ext, handlers := snapshot.ext, snapshot.handlers
		if len(handlers) == 0 {
			continue
		}
		for _, handler := range handlers {
			event := extension.BeforeProviderRequestEvent{
				Type:    "before_provider_request",
				Payload: current,
			}
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "before_provider_request", err)
				continue
			}
			// Upstream: `if (handlerResult !== undefined) currentPayload = handlerResult`.
			// In Go, nil is the closest equivalent of undefined; a handler
			// that genuinely wants to set payload to a typed nil should
			// return a non-nil any wrapping it.
			if handlerResult != nil {
				current = handlerResult
			}
		}
	}
	return current, nil
}

// EmitMessageEnd dispatches message_end to every handler in load order. Each
// handler sees the latest message; a handler may return a replacement with the
// same role. It returns the final replacement, or nil when no handler replaced
// the message. Handler errors and role changes are reported and skipped.
//
// upstream: runner.ts:1043-1080 (emitMessageEnd)
func (r *Runner) EmitMessageEnd(ctx context.Context, message extension.AgentMessage) (extension.AgentMessage, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}
	dispatchCtx := r.dispatchContext(ctx)
	current := message
	modified := false
	for _, snapshot := range snapshotEventHandlers(r.extensions, "message_end") {
		ext := snapshot.ext
		for _, handler := range snapshot.handlers {
			event := extension.MessageEndEvent{Type: "message_end", Message: current}
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "message_end", err)
				continue
			}
			if handlerResult == nil {
				continue
			}
			typed, ok := coerceResult[*extension.MessageEndEventResult](handlerResult)
			if !ok || typed == nil || typed.Message == nil || *typed.Message == nil {
				continue
			}
			if messageRole(*typed.Message) != messageRole(current) {
				r.recordHandlerError(ctx, ext.Path, "message_end", errors.New("message_end handlers must return a message with the same role"))
				continue
			}
			current = *typed.Message
			modified = true
		}
	}
	if !modified {
		return nil, nil
	}
	return current, nil
}

// messageRole reads the role of a host or wire message.
func messageRole(message extension.AgentMessage) string {
	if roled, ok := message.(interface{ Role() string }); ok {
		return roled.Role()
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return ""
	}
	var probe struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Role
}

// EmitBeforeProviderHeaders lets every "before_provider_headers" handler
// mutate the outgoing request headers in place; a nil value deletes a header.
// A subprocess handler cannot share the map, so a returned header map
// replaces the current headers instead. Handler errors are reported and the
// chain continues.
//
// upstream: runner.ts:1284-1310 (emitBeforeProviderHeaders)
func (r *Runner) EmitBeforeProviderHeaders(ctx context.Context, headers extension.ProviderHeaders) (extension.ProviderHeaders, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}
	if headers == nil {
		headers = extension.ProviderHeaders{}
	}
	dispatchCtx := r.dispatchContext(ctx)
	for _, snapshot := range snapshotEventHandlers(r.extensions, "before_provider_headers") {
		ext := snapshot.ext
		for _, handler := range snapshot.handlers {
			event := extension.BeforeProviderHeadersEvent{Type: "before_provider_headers", Headers: headers}
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "before_provider_headers", err)
				continue
			}
			if handlerResult == nil {
				continue
			}
			replacement, ok := coerceResult[extension.ProviderHeaders](handlerResult)
			if !ok {
				r.recordHandlerError(ctx, ext.Path, "before_provider_headers",
					fmt.Errorf("handler returned %T, expected the mutated headers", handlerResult))
				continue
			}
			clear(headers)
			maps.Copy(headers, replacement)
		}
	}
	return headers, nil
}

// EmitUserBash dispatches a user_bash event to every "user_bash" handler. The
// first handler that returns a result wins and later handlers do not run
// (first-wins, opposite of EmitToolCall).
//
// A handler error, or a result that is not exactly one valid operations or
// result object, is reported through emitError and returned, so callers fail
// closed instead of running the command locally (upstream #9068).
//
// Returns (nil, *StaleError) if the runner has been invalidated.
//
// upstream: runner.ts emitUserBash, isUserBashEventResult
func (r *Runner) EmitUserBash(ctx context.Context, event extension.UserBashEvent) (*extension.UserBashEventResult, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}

	dispatchCtx := r.dispatchContext(ctx)

	for _, snapshot := range snapshotEventHandlers(r.extensions, "user_bash") {
		ext := snapshot.ext
		for _, handler := range snapshot.handlers {
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err == nil && isUndefinedHandlerResult(handlerResult) {
				continue
			}
			var typed *extension.UserBashEventResult
			if err == nil {
				var ok bool
				typed, ok = coerceUserBashEventResult(handlerResult)
				if !ok || !isUserBashEventResult(typed) {
					err = errInvalidUserBashResult
				}
			}
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "user_bash", err)
				return nil, err
			}
			return typed, nil
		}
	}
	return nil, nil
}

var errInvalidUserBashResult = errors.New("Invalid user_bash handler result: return undefined for local execution or exactly one valid { operations } or { result } object")

// isUndefinedHandlerResult reports a handler result that is upstream's
// undefined: nothing returned, or a JSON null from a subprocess extension.
func isUndefinedHandlerResult(result any) bool {
	switch value := result.(type) {
	case nil:
		return true
	case json.RawMessage:
		return len(bytes.TrimSpace(value)) == 0 || string(bytes.TrimSpace(value)) == "null"
	case *json.RawMessage:
		return value == nil || len(bytes.TrimSpace(*value)) == 0 || string(bytes.TrimSpace(*value)) == "null"
	case *extension.UserBashEventResult:
		return value == nil
	}
	return false
}

// coerceUserBashEventResult preserves property presence before decoding optional Go fields.
// Ports packages/coding-agent/src/core/extensions/runner.ts (isUserBashEventResult).
func coerceUserBashEventResult(value any) (*extension.UserBashEventResult, bool) {
	var raw json.RawMessage
	switch v := value.(type) {
	case json.RawMessage:
		raw = v
	case *json.RawMessage:
		if v != nil {
			raw = *v
		}
	}
	if len(raw) > 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, false
		}
		_, hasOperations := fields["operations"]
		_, hasResult := fields["result"]
		if hasOperations == hasResult {
			return nil, false
		}
		if hasResult {
			var result map[string]any
			if err := json.Unmarshal(fields["result"], &result); err != nil || result == nil {
				return nil, false
			}
			exitCode, present := result["exitCode"]
			var undefined bool
			if marker, ok := fields["_pigUserBashExitCodeUndefined"]; ok {
				if err := json.Unmarshal(marker, &undefined); err != nil {
					return nil, false
				}
			}
			if undefined {
				if present {
					return nil, false
				}
				result["exitCode"] = nil
			} else if !present || exitCode == nil {
				return nil, false
			}
			if path, present := result["fullOutputPath"]; present && path == nil {
				return nil, false
			}
			return &extension.UserBashEventResult{Result: result}, true
		}
	}
	return coerceResult[*extension.UserBashEventResult](value)
}

// isUserBashEventResult requires exactly one executable operations or complete result object. A present nil native exit code represents the required number-or-undefined property. Raw JSON null remains invalid; the SDK marks undefined before serialization loses property presence.
// upstream: packages/coding-agent/src/core/extensions/runner.ts:isUserBashEventResult
func isUserBashEventResult(result *extension.UserBashEventResult) bool {
	if result == nil || (result.Operations == nil) == (result.Result == nil) {
		return false
	}
	if result.Operations != nil {
		return true
	}
	record, ok := result.Result.(map[string]any)
	if !ok {
		// A Go handler may return a typed result; judge its JSON form, which is
		// what a subprocess extension sends.
		encoded, err := json.Marshal(result.Result)
		if err != nil || json.Unmarshal(encoded, &record) != nil || record == nil {
			return false
		}
		result.Result = record
	}
	if _, ok := record["output"].(string); !ok {
		return false
	}
	if exitCode, present := record["exitCode"]; !present || (exitCode != nil && !isUserBashNumber(exitCode)) {
		return false
	}
	if _, ok := record["cancelled"].(bool); !ok {
		return false
	}
	if _, ok := record["truncated"].(bool); !ok {
		return false
	}
	if path, present := record["fullOutputPath"]; present && path != nil {
		if _, ok := path.(string); !ok {
			return false
		}
	}
	return true
}

func isUserBashNumber(value any) bool {
	if number, ok := value.(json.Number); ok {
		_, err := number.Float64()
		return err == nil
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// EmitBeforeAgentStart dispatches a before_agent_start event to every
// handler. Specialized aggregator: each handler can (a) push a custom
// message into the result; (b) mutate the system prompt for subsequent
// handlers. Returns the combined result, or nil if no handler modified
// state.
//
// **Upstream-specific shape**: the system prompt is mutable across the
// chain: each handler sees the LATEST mutated value via
// `event.SystemPrompt` and can override it via `result.SystemPrompt`.
// Subsequent handlers see the override. The final SystemPrompt in the
// returned result is the cumulatively-mutated value.
//
// The event and Context getter both expose the latest system prompt. Each
// handler's override becomes the input observed by the next handler.
//
// Every handler receives the same per-run copy of systemPromptOptions. Its
// section and selectedTools edits reach later handlers and the result, and
// they persist when the handler then fails.
//
// Returns (nil, *StaleError) if the runner has been invalidated.
//
// Handler errors route via emitError; chain continues.
//
// upstream: runner.ts:875-939 (emitBeforeAgentStart)
func (r *Runner) EmitBeforeAgentStart(
	ctx context.Context,
	prompt string,
	images []extension.ImageContent,
	systemPrompt string,
	systemPromptOptions extension.BuildSystemPromptOptions,
) (*extension.BeforeAgentStartCombinedResult, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}

	currentSystemPrompt := systemPrompt
	// runner.ts:1317 normalizeBuildSystemPromptOptions: handlers share one copy of the caller's options, so no edit reaches the caller's base object.
	currentOptions := clonePromptOptions(systemPromptOptions)
	hadSections := len(*currentOptions.Sections) > 0
	original := clonePromptOptions(*currentOptions)
	selectedBefore := slices.Clone(currentOptions.SelectedTools)
	promptCtx := extension.WithBeforeAgentStartOptions(ctx, currentOptions)
	var messages []extension.CustomMessageRef
	systemPromptModified := false

	for _, snapshot := range snapshotEventHandlers(r.extensions, "before_agent_start") {
		ext, handlers := snapshot.ext, snapshot.handlers
		if len(handlers) == 0 {
			continue
		}
		for _, handler := range handlers {
			actions := r.actions()
			actions.GetSystemPrompt = func() string { return currentSystemPrompt }
			extCtx := extension.NewContext(r.cwd, nil, r.assertActive, actions)
			dispatchCtx := extension.WithContext(promptCtx, extCtx)
			event := extension.BeforeAgentStartEvent{
				Type:                "before_agent_start",
				Prompt:              prompt,
				Images:              images,
				SystemPrompt:        currentSystemPrompt,
				SystemPromptOptions: *currentOptions,
			}
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "before_agent_start", err)
				continue
			}
			if handlerResult == nil {
				continue
			}
			typed, ok := coerceResult[*extension.BeforeAgentStartEventResult](handlerResult)
			if !ok {
				r.recordHandlerError(ctx, ext.Path, "before_agent_start",
					fmt.Errorf("handler returned %T, expected *extension.BeforeAgentStartEventResult", handlerResult))
				continue
			}
			if typed == nil {
				continue
			}
			if typed.Message != nil {
				messages = append(messages, *typed.Message)
			}
			// upstream: `if (result.systemPrompt !== undefined)`.
			if typed.SystemPrompt != nil {
				currentSystemPrompt = *typed.SystemPrompt
				currentOptions.ForceSystemPrompt = new(*typed.SystemPrompt)
				systemPromptModified = true
			}
		}
	}

	// A handler may edit or replace selectedTools; the Session decides whether the edit replaces the live loadout.
	selected, selectedEdited, err := extension.ResolveBeforeAgentStartSelectedTools(promptCtx, selectedBefore)
	if err != nil {
		return nil, err
	}
	currentOptions.SelectedTools = selected
	if currentOptions.Sections == nil {
		currentOptions.Sections = &ai.OrderedSections{}
	}
	hasSections := hadSections || len(*currentOptions.Sections) > 0
	// runner.ts:1462 returns the shared options object after every handler ran, and the run's prompt is built from it: an edit to any field, not only sections and selectedTools, reaches the request.
	optionsEdited := !reflect.DeepEqual(original, currentOptions)
	if len(messages) == 0 && !systemPromptModified && !hasSections && !selectedEdited && !optionsEdited {
		return nil, nil
	}
	combined := &extension.BeforeAgentStartCombinedResult{SelectedToolsEdited: selectedEdited}
	// A returned systemPrompt that a later handler cleared from the options leaves them unedited; they still decide the forced prompt (agent-session.ts:1724).
	if hasSections || selectedEdited || optionsEdited || systemPromptModified {
		combined.SystemPromptOptions = currentOptions
	}
	if len(messages) > 0 {
		combined.Messages = messages
	}
	if systemPromptModified {
		combined.SystemPrompt = &currentSystemPrompt
	}
	return combined, nil
}

// clonePromptOptions copies every collection that a handler can edit, as upstream normalizeBuildSystemPromptOptions does. Sections is always a distinct non-nil collection.
func clonePromptOptions(options extension.BuildSystemPromptOptions) *extension.BuildSystemPromptOptions {
	return new(extension.NormalizeBuildSystemPromptOptions(options))
}

// EmitResourcesDiscover dispatches a resources_discover event to every
// handler and aggregates the returned skill/prompt/theme paths,
// attributing each path to its source extension.
//
// Returns the aggregated result with empty (non-nil) slices when no
// handler returned any paths. Returns (nil, *StaleError) if the runner
// has been invalidated.
//
// Handler errors route via emitError; chain continues.
//
// upstream: runner.ts:941-988 (emitResourcesDiscover)
func (r *Runner) EmitResourcesDiscover(
	ctx context.Context,
	cwd string,
	reason string,
) (*extension.ResourcesDiscoverAggregateResult, error) {
	if err := r.assertActive(); err != nil {
		return nil, err
	}

	dispatchCtx := r.dispatchContext(ctx)
	out := &extension.ResourcesDiscoverAggregateResult{
		SkillPaths:  []extension.AttributedResourcePath{},
		PromptPaths: []extension.AttributedResourcePath{},
		ThemePaths:  []extension.AttributedResourcePath{},
	}

	for _, snapshot := range snapshotEventHandlers(r.extensions, "resources_discover") {
		ext, handlers := snapshot.ext, snapshot.handlers
		if len(handlers) == 0 {
			continue
		}
		for _, handler := range handlers {
			event := extension.ResourcesDiscoverEvent{
				Type:   "resources_discover",
				Cwd:    cwd,
				Reason: reason,
			}
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, "resources_discover", err)
				continue
			}
			if handlerResult == nil {
				continue
			}
			typed, ok := coerceResult[*extension.ResourcesDiscoverResult](handlerResult)
			if !ok {
				r.recordHandlerError(ctx, ext.Path, "resources_discover",
					fmt.Errorf("handler returned %T, expected *extension.ResourcesDiscoverResult", handlerResult))
				continue
			}

			for _, p := range typed.SkillPaths {
				out.SkillPaths = append(out.SkillPaths, extension.AttributedResourcePath{
					Path: p, ExtensionPath: ext.Path,
				})
			}
			for _, p := range typed.PromptPaths {
				out.PromptPaths = append(out.PromptPaths, extension.AttributedResourcePath{
					Path: p, ExtensionPath: ext.Path,
				})
			}
			for _, p := range typed.ThemePaths {
				out.ThemePaths = append(out.ThemePaths, extension.AttributedResourcePath{
					Path: p, ExtensionPath: ext.Path,
				})
			}
		}
	}
	return out, nil
}

// extractToolResultFields pulls the chainable fields (content, details,
// isError) out of any ToolResultEvent variant. Used by EmitToolResult
// to seed the mutation chain.
func extractToolResultFields(event extension.ToolResultEvent) (content []any, details any, isError bool, usage any, structured json.RawMessage) {
	switch e := event.(type) {
	case extension.BashToolResultEvent:
		return e.Content, e.Details, e.IsError, e.Usage, e.StructuredContent
	case extension.PowerShellToolResultEvent:
		return e.Content, e.Details, e.IsError, e.Usage, e.StructuredContent
	case extension.ReadToolResultEvent:
		return e.Content, e.Details, e.IsError, e.Usage, e.StructuredContent
	case extension.EditToolResultEvent:
		return e.Content, e.Details, e.IsError, e.Usage, e.StructuredContent
	case extension.WriteToolResultEvent:
		return e.Content, e.Details, e.IsError, e.Usage, e.StructuredContent
	case extension.GrepToolResultEvent:
		return e.Content, e.Details, e.IsError, e.Usage, e.StructuredContent
	case extension.FindToolResultEvent:
		return e.Content, e.Details, e.IsError, e.Usage, e.StructuredContent
	case extension.LsToolResultEvent:
		return e.Content, e.Details, e.IsError, e.Usage, e.StructuredContent
	case extension.CustomToolResultEvent:
		return e.Content, e.Details, e.IsError, e.Usage, e.StructuredContent
	}
	return nil, nil, false, nil, nil
}

// withToolResultFields returns event with the chained content, details,
// isError and usage. A built-in variant keeps its typed details only when a
// handler's replacement converts to them without loss; otherwise the event
// continues as a CustomToolResultEvent with the same toolName carrying the
// replacement exactly, as upstream assigns handlerResult.details as returned.
func withToolResultFields(event extension.ToolResultEvent, content []any, details any, isError bool, usage any, structured json.RawMessage) extension.ToolResultEvent {
	base := func(b extension.ToolResultEventBase) extension.ToolResultEventBase {
		b.Content, b.IsError, b.Usage, b.StructuredContent = content, isError, usage, structured
		return b
	}
	generic := func(b extension.ToolResultEventBase, toolName string) extension.ToolResultEvent {
		return extension.CustomToolResultEvent{ToolResultEventBase: base(b), ToolName: toolName, Details: details}
	}
	switch e := event.(type) {
	case extension.BashToolResultEvent:
		if typed, ok := typedDetails[extension.BashToolDetails](details); ok {
			e.ToolResultEventBase, e.Details = base(e.ToolResultEventBase), typed
			return e
		}
		return generic(e.ToolResultEventBase, e.ToolName)
	case extension.PowerShellToolResultEvent:
		if typed, ok := typedDetails[extension.PowerShellToolDetails](details); ok {
			e.ToolResultEventBase, e.Details = base(e.ToolResultEventBase), typed
			return e
		}
		return generic(e.ToolResultEventBase, e.ToolName)
	case extension.ReadToolResultEvent:
		if typed, ok := typedDetails[extension.ReadToolDetails](details); ok {
			e.ToolResultEventBase, e.Details = base(e.ToolResultEventBase), typed
			return e
		}
		return generic(e.ToolResultEventBase, e.ToolName)
	case extension.EditToolResultEvent:
		if typed, ok := typedDetails[extension.EditToolDetails](details); ok {
			e.ToolResultEventBase, e.Details = base(e.ToolResultEventBase), typed
			return e
		}
		return generic(e.ToolResultEventBase, e.ToolName)
	case extension.WriteToolResultEvent:
		e.ToolResultEventBase, e.Details = base(e.ToolResultEventBase), details
		return e
	case extension.GrepToolResultEvent:
		if typed, ok := typedDetails[extension.GrepToolDetails](details); ok {
			e.ToolResultEventBase, e.Details = base(e.ToolResultEventBase), typed
			return e
		}
		return generic(e.ToolResultEventBase, e.ToolName)
	case extension.FindToolResultEvent:
		if typed, ok := typedDetails[extension.FindToolDetails](details); ok {
			e.ToolResultEventBase, e.Details = base(e.ToolResultEventBase), typed
			return e
		}
		return generic(e.ToolResultEventBase, e.ToolName)
	case extension.LsToolResultEvent:
		if typed, ok := typedDetails[extension.LsToolDetails](details); ok {
			e.ToolResultEventBase, e.Details = base(e.ToolResultEventBase), typed
			return e
		}
		return generic(e.ToolResultEventBase, e.ToolName)
	case extension.CustomToolResultEvent:
		e.ToolResultEventBase, e.Details = base(e.ToolResultEventBase), details
		return e
	}
	return event
}

// typedDetails converts details to *T and reports whether that is lossless:
// the typed value encodes to the same JSON value as details.
func typedDetails[T any](details any) (*T, bool) {
	switch value := details.(type) {
	case nil:
		return nil, true
	case *T:
		return value, true
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return nil, false
	}
	out := new(T)
	if json.Unmarshal(raw, out) != nil {
		return nil, false
	}
	back, err := json.Marshal(out)
	if err != nil {
		return nil, false
	}
	var want, got any
	if json.Unmarshal(raw, &want) != nil || json.Unmarshal(back, &got) != nil || !reflect.DeepEqual(want, got) {
		return nil, false
	}
	return out, true
}

// Emit dispatches a generic ExtensionEvent to every handler registered
// for the event's type, in load order. Used for events that don't have
// a dedicated EmitXxx method: session_start, session_shutdown,
// session_before_switch/fork/compact/tree, session_compact,
// session_tree, agent_start/end, turn_start/end, message_start/update/end,
// tool_execution_start/update/end, model_select, after_provider_response.
//
// Result type:
//   - For SessionBefore* events (Switch/Fork/Compact/Tree): if any handler
//     returns a result with Cancel=true, dispatch halts and that result is
//     returned. Otherwise the LAST non-nil result is returned. Callers
//     type-assert against the appropriate *SessionBefore*Result.
//   - For all other events: returns nil after running all handlers.
//
// An invalidated runner still delivers the event, as upstream emit does; the
// ctx getters and actions a handler uses report the stale runner.
//
// Handler errors are routed via emitError and the chain continues
// (matches upstream's try/catch at runner.ts:691-699). An invocation acknowledgment marks the first handler's return or runtime suspension; Emit itself still awaits the entire chain.
//
// upstream: runner.ts:988-1017 (emit<TEvent>)
//
// pig note: upstream uses TS conditional types (`RunnerEmitResult<TEvent>`)
// for type-narrowing the result based on the input event type. Go's type
// system can't express this, so the result is `any` and callers
// type-assert. The 4 SessionBefore* event types are documented as the
// only types whose result is non-nil in the godoc above.
func (r *Runner) Emit(ctx context.Context, event any) (any, error) {
	if event == nil {
		return nil, nil
	}

	eventType, err := readEventType(event)
	if err != nil {
		return nil, err
	}

	dispatchCtx := r.dispatchContext(ctx)
	isSessionBefore := isSessionBeforeEvent(event)
	var result any

	for _, snapshot := range snapshotEventHandlers(r.extensions, eventType) {
		ext, handlers := snapshot.ext, snapshot.handlers
		if len(handlers) == 0 {
			continue
		}
		for _, handler := range handlers {
			handlerResult, err := callHandler(handler, event, dispatchCtx)
			if err != nil {
				r.recordHandlerError(ctx, ext.Path, eventType, err)
				invocation.Acknowledge(dispatchCtx)
				continue
			}
			// Upstream runner.ts:emit suspends at await handler even when the handler returns synchronously.
			invocation.Acknowledge(dispatchCtx)
			if handlerResult == nil {
				continue
			}
			if isSessionBefore {
				result = handlerResult
				if sessionBeforeIsCancel(handlerResult) {
					return handlerResult, nil
				}
			}
		}
	}
	return result, nil
}

// callHandler invokes an extension event handler, converting a panic into an
// error so a single misbehaving extension cannot crash the host process and
// leave the terminal in raw mode. Mirrors upstream runner.ts, which wraps
// every handler dispatch in try/catch and routes throws to emitError
// (runner.ts:702-714). Callers already record the returned error via
// recordHandlerError, so a panicking handler is isolated and surfaced the
// same way a returned error is.
func callHandler(handler extension.HandlerFn, args ...any) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = &handlerPanicError{
				message: fmt.Sprintf("extension handler panicked: %v", r),
				stack:   string(debug.Stack()),
			}
		}
	}()
	result, err = handler(args...)
	return noResultAsNil(result), err
}

// handlerPanicError is a recovered handler panic. Its stack is the panicking
// goroutine's, the Go counterpart of a thrown JavaScript error's `stack`.
type handlerPanicError struct {
	message string
	stack   string
}

func (e *handlerPanicError) Error() string { return e.message }

// ErrorStack returns the stack as upstream formats `err.stack`: the message
// line first, then the frames.
func (e *handlerPanicError) ErrorStack() string { return e.message + "\n" + e.stack }

// noResultAsNil maps every encoding of "the handler returned nothing" to nil:
// a JSON null from a subprocess handler that returned undefined or None, and a
// typed nil pointer from an in-process handler. Upstream skips an undefined
// handler result, so none of these may reach a typed result dereference.
func noResultAsNil(result any) any {
	switch value := result.(type) {
	case nil:
		return nil
	case json.RawMessage:
		if isJSONNull(value) {
			return nil
		}
	case *json.RawMessage:
		if value == nil || isJSONNull(*value) {
			return nil
		}
	default:
		if v := reflect.ValueOf(result); v.Kind() == reflect.Pointer && v.IsNil() {
			return nil
		}
	}
	return result
}

func isJSONNull(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

// readEventType extracts the `Type` string field from an event struct
// via reflection. The field is present on every ExtensionEvent variant
// (mirrors upstream's discriminator). Returns an error if the value
// has no `Type` field of kind string.
//
// pig-internal: this is the one reflection use in the runner's hot
// path. Piglet if it ever shows up in flame graphs; the alternative
// is adding an `EventType() string` method to ~20 event structs which
// is invasive and clutters the wire-format types.
func readEventType(event any) (string, error) {
	v := reflect.ValueOf(event)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", fmt.Errorf("event is nil pointer")
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return "", fmt.Errorf("event is %T, expected struct with Type field", event)
	}
	f := v.FieldByName("Type")
	if !f.IsValid() || f.Kind() != reflect.String {
		return "", fmt.Errorf("event %T has no Type string field", event)
	}
	type_ := f.String()
	if type_ == "" {
		return "", fmt.Errorf("event %T has empty Type field", event)
	}
	return type_, nil
}

// isSessionBeforeEvent reports whether the event is one of the 4
// SessionBefore* variants whose dispatch supports a Cancel
// short-circuit.
//
// upstream: runner.ts:664-672 (isSessionBeforeEvent)
func isSessionBeforeEvent(event any) bool {
	switch event.(type) {
	case extension.SessionBeforeSwitchEvent, *extension.SessionBeforeSwitchEvent,
		extension.SessionBeforeForkEvent, *extension.SessionBeforeForkEvent,
		extension.SessionBeforeCompactEvent, *extension.SessionBeforeCompactEvent,
		extension.SessionBeforeTreeEvent, *extension.SessionBeforeTreeEvent:
		return true
	}
	return false
}

// sessionBeforeIsCancel reports whether a handler result for a
// SessionBefore* event signals cancellation. Mirrors upstream's
// `result.cancel` check (runner.ts:687).
func sessionBeforeIsCancel(result any) bool {
	// Handle direct type assertions first.
	switch r := result.(type) {
	case *extension.SessionBeforeSwitchResult:
		return r != nil && r.Cancel
	case extension.SessionBeforeSwitchResult:
		return r.Cancel
	case *extension.SessionBeforeForkResult:
		return r != nil && r.Cancel
	case extension.SessionBeforeForkResult:
		return r.Cancel
	case *extension.SessionBeforeCompactResult:
		return r != nil && r.Cancel
	case extension.SessionBeforeCompactResult:
		return r.Cancel
	case *extension.SessionBeforeTreeResult:
		return r != nil && r.Cancel
	case extension.SessionBeforeTreeResult:
		return r.Cancel
	}
	// Subprocess extensions return json.RawMessage; probe the Cancel field.
	var raw json.RawMessage
	switch v := result.(type) {
	case json.RawMessage:
		raw = v
	case *json.RawMessage:
		if v != nil {
			raw = *v
		}
	}
	if len(raw) > 0 {
		var probe struct {
			Cancel bool `json:"cancel"`
		}
		if json.Unmarshal(raw, &probe) == nil {
			return probe.Cancel
		}
	}
	return false
}
