package extension

import "context"

// Context is the per-extension runtime context. Mirrors upstream's
// ExtensionContext (types.ts:ExtensionContext).
//
// Authors retrieve the Context from a context.Context via FromContext.
// The host attaches it at event-dispatch time using WithContext.
//
// Go carries cancellation and per-extension values through one context.Context.
// Authors retrieve the extension Context with [FromContext].
//
// Context exposes the runtime, UI, session, model, and cancellation surfaces
// backed by ContextActions. Optional actions use their documented zero-value
// behavior when the host does not bind them.
type Context struct {
	// Unexported anchor field. Forces hosts to use a real allocator
	// (the in-process runner builds Contexts via internal helpers);
	// extensions cannot zero-value a Context that the host didn't
	// create.
	_ noCopy

	// cwd is the working directory captured at Runner construction.
	// Mirrors upstream `get cwd()` (runner.ts:581).
	cwd string

	// uiContext is the per-mode UI context, captured at
	// createContext time. Mirrors upstream's `runner.uiContext`
	// access pattern (runner.ts:572-575). Default value is
	// [NoopUIContext] so callers never need a nil check.
	//
	// **Identity contract:** Context.HasUI() reports true iff
	// `uiContext != NoopUIContext` (pointer identity, runner.ts:361).
	uiContext UIContext

	// assertActive is the runner's assertActive function, captured at
	// createContext time. Every getter/method on Context calls this
	// first to reject stale contexts.
	//
	// upstream: every getter in createContext calls `runner.assertActive()`
	// (runner.ts:571-637).
	assertActive func() error

	// actions carries the host-injected callbacks that back the
	// dynamic Context surfaces (Model, IsIdle, Shutdown, etc.).
	// Mirrors upstream's read of `runner.<field>` inside each
	// `createContext` getter: each Go method calls assertActive, then
	// invokes actions.<Field> or returns that action's documented default.
	//
	// upstream: runner.ts:567 (createContext closes over runner.<field>)
	actions ContextActions
}

// CWD returns the working directory captured at Runner construction.
// Returns an error if the runner has been invalidated.
//
// upstream: runner.ts:581 (`get cwd()`)
func (c *Context) CWD() (string, error) {
	if err := c.assertActive(); err != nil {
		return "", err
	}
	return c.cwd, nil
}

// UI returns the per-mode UIContext. Returns the package-level
// [NoopUIContext] singleton when no UI was bound (matches upstream's
// pre-bind state at runner.ts:255).
//
// Returns an error if the runner has been invalidated.
//
// upstream: runner.ts:572-575 (`get ui()` returns `runner.uiContext`)
func (c *Context) UI() (UIContext, error) {
	if err := c.assertActive(); err != nil {
		return nil, err
	}
	return c.uiContext, nil
}

// HasUI reports whether the runner has a real (non-noop) UI context
// attached. Mirrors upstream's pointer-identity check
// (runner.ts:361: `this.uiContext !== noOpUIContext`).
//
// Returns an error if the runner has been invalidated.
//
// upstream: runner.ts:359-362
func (c *Context) HasUI() (bool, error) {
	if err := c.assertActive(); err != nil {
		return false, err
	}
	return c.uiContext != NoopUIContext, nil
}

// SessionManager returns the per-runtime SessionManager. Returns an
// error if the runner has been invalidated.
//
// SessionManager is opaque at the extension boundary; the host owns its
// concrete implementation.
//
// upstream: runner.ts:582 (`get sessionManager()`)
func (c *Context) SessionManager() (SessionManager, error) {
	if err := c.assertActive(); err != nil {
		return nil, err
	}
	return c.actions.SessionManager, nil
}

// ModelRegistry returns the per-runtime ModelRegistry. Returns an
// error if the runner has been invalidated.
//
// upstream: runner.ts:586 (`get modelRegistry()`)
func (c *Context) ModelRegistry() (ModelRegistry, error) {
	if err := c.assertActive(); err != nil {
		return nil, err
	}
	return c.actions.ModelRegistry, nil
}

// Model returns the currently selected model, or nil if none is set.
// Returns an error if the runner has been invalidated.
//
// Model is opaque at the extension boundary so providers retain their
// concrete model representation.
//
// upstream: runner.ts:590 (`get model()`)
func (c *Context) Model() (Model, error) {
	if err := c.assertActive(); err != nil {
		return nil, err
	}
	if c.actions.GetModel == nil {
		return nil, nil
	}
	return c.actions.GetModel(), nil
}

// IsIdle reports whether the agent is currently idle (not streaming).
// Returns an error if the runner has been invalidated.
//
// Default semantics when no actions injector is bound: returns true
// (matches upstream's `() => true` default at runner.ts:228).
//
// upstream: runner.ts:594-597 (`isIdle: () => { ... return runner.isIdleFn(); }`)
func (c *Context) IsIdle() (bool, error) {
	if err := c.assertActive(); err != nil {
		return false, err
	}
	if c.actions.IsIdle == nil {
		return true, nil
	}
	return c.actions.IsIdle(), nil
}

// IsProjectTrusted reports whether the current project is trusted, so an
// extension can refuse to read project-scoped configuration or run project
// code before the user has trusted it.
// Returns an error if the runner has been invalidated.
//
// Default semantics when no actions injector is bound: returns true
// (matches upstream's `() => true` default at runner.ts:280).
//
// upstream: runner.ts:718-721 (`isProjectTrusted: () => { ... }`)
func (c *Context) IsProjectTrusted() (bool, error) {
	if err := c.assertActive(); err != nil {
		return false, err
	}
	if c.actions.IsProjectTrusted == nil {
		return true, nil
	}
	return c.actions.IsProjectTrusted(), nil
}

// HasPendingMessages reports whether queued messages are awaiting
// processing. Returns an error if the runner has been invalidated.
//
// Default semantics when no actions injector is bound: returns false
// (matches upstream's `() => false` default at runner.ts:232).
//
// upstream: runner.ts:606-609 (`hasPendingMessages: () => { ... }`)
func (c *Context) HasPendingMessages() (bool, error) {
	if err := c.assertActive(); err != nil {
		return false, err
	}
	if c.actions.HasPendingMessages == nil {
		return false, nil
	}
	return c.actions.HasPendingMessages(), nil
}

// Abort cancels the current agent operation. Returns an error if the
// runner has been invalidated.
//
// Default semantics when no actions injector is bound: no-op
// (matches upstream's `() => {}` default at runner.ts:240).
//
// upstream: runner.ts:602-605 (`abort: () => { ... }`)
func (c *Context) Abort() error {
	if err := c.assertActive(); err != nil {
		return err
	}
	if c.actions.Abort != nil {
		c.actions.Abort()
	}
	return nil
}

// Shutdown triggers graceful agent shutdown. Returns an error if the
// runner has been invalidated.
//
// Default semantics when no actions injector is bound: no-op
// (matches upstream's `() => {}` default at runner.ts:241).
//
// upstream: runner.ts:610-613 (`shutdown: () => { ... }`)
func (c *Context) Shutdown() error {
	if err := c.assertActive(); err != nil {
		return err
	}
	if c.actions.Shutdown != nil {
		c.actions.Shutdown()
	}
	return nil
}

// GetContextUsage returns the current context-window usage for the
// active model, or nil if unknown. Returns an error if the runner
// has been invalidated.
//
// Default semantics when no actions injector is bound: returns nil
// (matches upstream's `() => undefined` default at runner.ts:233).
//
// upstream: runner.ts:614-617 (`getContextUsage: () => { ... }`)
func (c *Context) GetContextUsage() (*ContextUsage, error) {
	if err := c.assertActive(); err != nil {
		return nil, err
	}
	if c.actions.GetContextUsage == nil {
		return nil, nil
	}
	return c.actions.GetContextUsage(), nil
}

// GetSystemPrompt returns the current system prompt text. Returns an
// error if the runner has been invalidated.
//
// Default semantics when no actions injector is bound: returns the
// empty string (matches upstream's `() => ""` default).
//
// upstream: runner.ts:622-625 (`getSystemPrompt: () => { ... }`)
func (c *Context) GetSystemPrompt() (string, error) {
	if err := c.assertActive(); err != nil {
		return "", err
	}
	if c.actions.GetSystemPrompt == nil {
		return "", nil
	}
	return c.actions.GetSystemPrompt(), nil
}

// Compact triggers compaction without awaiting completion. Returns
// an error if the runner has been invalidated.
//
// Default semantics when no actions injector is bound: no-op
// (matches upstream's `() => {}` default at runner.ts:234).
//
// upstream: runner.ts:618-621 (`compact: (options) => { ... }`)
func (c *Context) Compact(opts *CompactOptions) error {
	if err := c.assertActive(); err != nil {
		return err
	}
	if c.actions.Compact != nil {
		c.actions.Compact(opts)
	}
	return nil
}

// Mode returns the run mode pi is operating in (tui/rpc/json/print).
// Extensions guard terminal-only UI on mode == ModeTUI. The empty
// (unset) value normalizes to ModePrint, matching upstream's Runner
// default. Returns an error if the runner has been invalidated.
//
// upstream: runner.ts:586-588 (`get mode() { ... return runner.mode; }`),
// types.ts:304
func (c *Context) Mode() (ExtensionMode, error) {
	if err := c.assertActive(); err != nil {
		return "", err
	}
	return c.actions.Mode.normalize(), nil
}

// pig additive (D23): these methods support Piglet tool scoping.
// GetAllTools returns metadata about all registered tools.
// Returns nil if no tool-scoping actions are wired.
func (c *Context) GetAllTools() []ToolInfo {
	if err := c.assertActive(); err != nil {
		return nil
	}
	if c.actions.GetAllTools != nil {
		return c.actions.GetAllTools()
	}
	return nil
}

// GetActiveTools returns the names of currently active tools.
// Returns nil if no tool-scoping actions are wired.
func (c *Context) GetActiveTools() []string {
	if err := c.assertActive(); err != nil {
		return nil
	}
	if c.actions.GetActiveTools != nil {
		return c.actions.GetActiveTools()
	}
	return nil
}

// SetActiveTools sets the active tool list by name.
// No-op if no tool-scoping actions are wired.
func (c *Context) SetActiveTools(names []string) {
	if err := c.assertActive(); err != nil {
		return
	}
	if c.actions.SetActiveTools != nil {
		c.actions.SetActiveTools(names)
	}
}

// GetFlagValue returns the value of an extension-registered flag.
// Returns nil if no flag-value actions are wired.
func (c *Context) GetFlagValue(name string) any {
	if err := c.assertActive(); err != nil {
		return nil
	}
	if c.actions.GetFlagValue != nil {
		return c.actions.GetFlagValue(name)
	}
	return nil
}

// ─── context.Context plumbing ─────────────────────────────────────────────

// noCopy is a zero-sized marker type that signals to `go vet -copylocks`
// that values of the containing type should not be copied. The Context
// is owned by the host's runtime; extensions get a *Context, never a
// Context by value.
type noCopy struct{}

// Lock is a no-op method used by `go vet -copylocks` to detect copies.
func (noCopy) Lock() {}

// Unlock is the matching no-op for the Lock method.
func (noCopy) Unlock() {}

type ctxKey struct{}

// WithContext attaches a per-extension Context to a context.Context.
// Hosts call this once at event-dispatch time; the resulting
// context.Context is then threaded through to extension handlers.
//
// Authors do not call WithContext directly.
func WithContext(parent context.Context, c *Context) context.Context {
	return context.WithValue(parent, ctxKey{}, c)
}

// FromContext retrieves the per-extension Context that the host
// attached via WithContext. Returns nil if none was attached (e.g. a
// unit test that constructs handlers without a host).
//
// Typical usage in an extension handler:
//
//	api.OnToolCall(func(ctx context.Context, event extension.ToolCallEvent) extension.ToolCallEventResult {
//	    extCtx := extension.FromContext(ctx)
//	    if extCtx != nil {
//	        cwd, _ := extCtx.CWD()
//	        // ... use cwd
//	    }
//	    return extension.ToolCallEventResult{}
//	})
func FromContext(ctx context.Context) *Context {
	c, _ := ctx.Value(ctxKey{}).(*Context)
	return c
}

// NewContext constructs a Context with the given parameters. This is
// the constructor that hosts (Runner) use; extension authors never
// call this directly.
//
// uiContext is the per-mode UI binding (matches upstream's
// `runner.uiContext` field at runner.ts:225). Pass [NoopUIContext]
// when no UI is available; nil is normalized to NoopUIContext so
// callers of [Context.UI] always receive a non-nil dispatchable.
//
// assertActive is the runner's staleness guard; every getter/method on
// Context calls it first.
//
// actions carries the optional injection callbacks that back
// Model/IsIdle/Shutdown/etc. Pass a zero-value `ContextActions{}` for
// the no-op defaults (matches upstream's bindCore-pre-bind state).
//
// upstream: runner.ts:566-630 (createContext factory)
func NewContext(cwd string, uiContext UIContext, assertActive func() error, actions ContextActions) *Context {
	if uiContext == nil {
		uiContext = NoopUIContext
	}
	return &Context{
		cwd:          cwd,
		uiContext:    uiContext,
		assertActive: assertActive,
		actions:      actions,
	}
}

// SendUserMessage injects a user message into the agent loop and triggers a
// turn when the agent is idle.
//
// Upstream exposes this method on ExtensionAPI. Go handlers receive Context
// directly, so the method is available here.
func (c *Context) SendUserMessage(content any, opts *SendUserMessageOptions) error {
	if err := c.assertActive(); err != nil {
		return err
	}
	if c.actions.SendUserMessage == nil {
		return nil
	}
	return c.actions.SendUserMessage(content, opts)
}

// ─── CommandContext ───────────────────────────────────────────────────────────

// CommandContext extends Context with command-specific actions available
// only inside slash-command handlers. Mirrors upstream
// ExtensionCommandContext (types.ts:328-363).
//
// Retrieve from a context.Context via [CommandContextFromContext].
//
// upstream: runner.ts:736-772 (createCommandContext)
type CommandContext struct {
	*Context
	cmdActions CommandActions
}

// NewCommandContext constructs a CommandContext from the base context
// and command-specific actions. Called by the runner when dispatching
// a slash command registered by an extension. SendUserMessage is inherited
// from the base Context, which carries it for every dispatch.
//
// upstream: runner.ts:736-772
func NewCommandContext(base *Context, cmdActions CommandActions) *CommandContext {
	return &CommandContext{Context: base, cmdActions: cmdActions}
}

// WaitForIdle blocks until the agent finishes streaming.
func (c *CommandContext) WaitForIdle() error {
	if err := c.assertActive(); err != nil {
		return err
	}
	if c.cmdActions.WaitForIdle == nil {
		return nil
	}
	return c.cmdActions.WaitForIdle()
}

// NewSession starts a new session.
func (c *CommandContext) NewSession(opts *NewSessionOptions) (CancelledResult, error) {
	if err := c.assertActive(); err != nil {
		return CancelledResult{Cancelled: true}, err
	}
	if c.cmdActions.NewSession == nil {
		return CancelledResult{Cancelled: true}, nil
	}
	return c.cmdActions.NewSession(opts)
}

// Fork creates a new branch from entryID.
func (c *CommandContext) Fork(entryID string, opts *ForkOptions) (CancelledResult, error) {
	if err := c.assertActive(); err != nil {
		return CancelledResult{Cancelled: true}, err
	}
	if c.cmdActions.Fork == nil {
		return CancelledResult{Cancelled: true}, nil
	}
	return c.cmdActions.Fork(entryID, opts)
}

// NavigateTree moves to a different point in the session tree.
func (c *CommandContext) NavigateTree(targetID string, opts *NavigateTreeOptions) (CancelledResult, error) {
	if err := c.assertActive(); err != nil {
		return CancelledResult{Cancelled: true}, err
	}
	if c.cmdActions.NavigateTree == nil {
		return CancelledResult{Cancelled: true}, nil
	}
	return c.cmdActions.NavigateTree(targetID, opts)
}

// SwitchSession switches to a different session file.
func (c *CommandContext) SwitchSession(sessionPath string, opts *SwitchSessionOptions) (CancelledResult, error) {
	if err := c.assertActive(); err != nil {
		return CancelledResult{Cancelled: true}, err
	}
	if c.cmdActions.SwitchSession == nil {
		return CancelledResult{Cancelled: true}, nil
	}
	return c.cmdActions.SwitchSession(sessionPath, opts)
}

// Reload reloads extensions, skills, prompts, themes.
func (c *CommandContext) Reload() error {
	if err := c.assertActive(); err != nil {
		return err
	}
	if c.cmdActions.Reload == nil {
		return nil
	}
	return c.cmdActions.Reload()
}

// GetSystemPromptOptions returns the base inputs pi currently uses to
// build the system prompt (custom prompt, active tools, tool snippets,
// prompt guidelines, appended text, cwd, loaded context files, and
// skills). It reports the current base inputs only; it does not include
// per-turn before_agent_start chained changes, later context-event
// message mutations, or before_provider_request rewrites.
//
// When no data source is wired, returns zero-value options carrying the
// runner cwd, matching upstream's
// `getSystemPromptOptions ?? (() => ({ cwd: this.cwd }))` default.
//
// upstream: runner.ts:653-656 (createCommandContext getSystemPromptOptions),
// types.ts:339
func (c *CommandContext) GetSystemPromptOptions() (BuildSystemPromptOptions, error) {
	if err := c.assertActive(); err != nil {
		return BuildSystemPromptOptions{}, err
	}
	if c.actions.GetSystemPromptOptions == nil {
		return BuildSystemPromptOptions{Cwd: c.cwd}, nil
	}
	return c.actions.GetSystemPromptOptions(), nil
}

// ─── CommandContext key ───────────────────────────────────────────────────────

type cmdCtxKey struct{}

// WithCommandContext attaches a [CommandContext] to a context.Context.
func WithCommandContext(ctx context.Context, cc *CommandContext) context.Context {
	return context.WithValue(ctx, cmdCtxKey{}, cc)
}

// CommandContextFromContext retrieves the [CommandContext] attached
// by [WithCommandContext]. Returns nil if none is set.
func CommandContextFromContext(ctx context.Context) *CommandContext {
	cc, _ := ctx.Value(cmdCtxKey{}).(*CommandContext)
	return cc
}
