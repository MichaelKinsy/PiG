package extension

import "context"

// ContextUsage mirrors upstream `ContextUsage` (types.ts:276-283). Reports
// the agent's current context-window utilization for the active model.
//
// Field semantics (from upstream JSDoc):
//
//   - Tokens: estimated context tokens, or nil if unknown (e.g. right
//     after compaction, before the next LLM response).
//   - ContextWindow: model's maximum context size in tokens.
//   - Percent: usage as percentage of context window, or nil if Tokens
//     is unknown.
//
// Pointer-typed nullable fields mirror upstream's `number | null`
// shape; using `*int` rather than `int` distinguishes "unknown" from
// "zero".
//
// upstream: types.ts:276-283
type ContextUsage struct {
	Tokens        *int `json:"tokens"`
	ContextWindow int  `json:"contextWindow"`
	Percent       *int `json:"percent"`
}

// CompactOptions mirrors upstream `CompactOptions` (types.ts:284-288).
// Configures a compaction operation triggered via Context.Compact.
//
//   - CustomInstructions: optional user-provided guidance for the
//     summarization model.
//   - OnComplete: callback invoked with the CompactionResult once the
//     async compaction finishes. Nil disables the callback (fire-and-
//     forget).
//   - OnError: callback invoked when compaction fails. Nil routes the
//     error to the runner's error listeners.
//
// upstream: types.ts:284-288
type CompactOptions struct {
	CustomInstructions string                        `json:"customInstructions,omitempty"`
	OnComplete         func(result CompactionResult) `json:"-"`
	OnError            func(err error)               `json:"-"`
}

// ContextActions is the host-side injection of per-runtime callbacks
// that back Context's surfaces. Mirrors upstream's
// `ExtensionContextActions` (types.ts:1467-1481): the single struct
// passed to `bindCore` to wire dynamic context surfaces (model
// selection, idle state, abort handling, etc.) without making the
// Context constructor signature unwieldy.
//
// All fields are optional. Nil function fields produce upstream-
// equivalent default behavior:
//
//	GetModel              nil → Model() returns nil (upstream: () => undefined)
//	IsIdle                nil → IsIdle() returns true (upstream: () => true)
//	HasPendingMessages    nil → HasPendingMessages() returns false (upstream: () => false)
//	Shutdown              nil → Shutdown() is no-op (upstream: () => {})
//	GetContextUsage       nil → GetContextUsage() returns nil (upstream: () => undefined)
//	Compact               nil → Compact() is no-op (upstream: () => {})
//	GetSystemPrompt       nil → GetSystemPrompt() returns "" (upstream: () => "")
//
// SessionManager and ModelRegistry are direct value injections (not
// closures) because upstream stores them as Runner fields, not
// callbacks; nil values are honored.
//
// pig additive (D23): ContextActions supplies Piglet tool-scoping operations.
type ContextActions struct {
	// SessionManager backs Context.SessionManager().
	// upstream: types.ts:301 (read-only)
	SessionManager SessionManager

	// ModelRegistry backs Context.ModelRegistry().
	// upstream: types.ts:303
	ModelRegistry ModelRegistry

	// GetModel backs Context.Model(). Returns nil if no model is
	// currently selected.
	// upstream: types.ts:305 (Model<any> | undefined)
	GetModel func() Model

	// IsIdle backs Context.IsIdle().
	// upstream: types.ts:307
	IsIdle func() bool

	// IsProjectTrusted backs Context.IsProjectTrusted().
	// upstream: types.ts:332
	IsProjectTrusted func() bool

	// HasPendingMessages backs Context.HasPendingMessages().
	// upstream: types.ts:313
	HasPendingMessages func() bool

	// SendUserMessage backs Context.SendUserMessage(). Injects a user
	// message into the agent loop. Populated by the runner from
	// ExtensionActions, which is where a host supplies it.
	// upstream: types.ts:1306 (ExtensionAPI.sendUserMessage)
	SendUserMessage SendUserMessageHandler

	// Abort backs Context.Abort(). Cancels the current agent action.
	// upstream: types.ts:311
	Abort func()

	// Shutdown backs Context.Shutdown(). Triggers graceful agent
	// shutdown.
	// upstream: types.ts:315
	Shutdown func()

	// GetContextUsage backs Context.GetContextUsage().
	// upstream: types.ts:317
	GetContextUsage func() *ContextUsage

	// Compact backs Context.Compact(opts).
	// upstream: types.ts:319
	Compact func(opts *CompactOptions)

	// GetSystemPrompt backs Context.GetSystemPrompt(). The host callback
	// returns the latest prompt, including changes made by earlier
	// before_agent_start handlers.
	// upstream: types.ts:321
	GetSystemPrompt func() string

	// GetSystemPromptOptions backs CommandContext.GetSystemPromptOptions().
	// Returns the base inputs pi currently uses to build the system
	// prompt (custom prompt, tools, snippets, guidelines, append text,
	// cwd, context files, skills). nil → method returns zero-value
	// options, matching upstream's
	// `getSystemPromptOptions ?? (() => ({ cwd: this.cwd }))` default.
	// upstream: types.ts:1512, runner.ts:303,653
	GetSystemPromptOptions func() BuildSystemPromptOptions

	// Mode backs Context.Mode(). The empty value normalizes to
	// ModePrint, matching upstream's Runner default (runner.ts:229).
	// upstream: types.ts:304 (`mode: ExtensionMode`)
	Mode ExtensionMode

	// UI backs Context.UI(). Must be non-nil; pass
	// [NoopUIContext] when no UI is available.
	UI UIContext

	// GetAllTools returns metadata about all registered tools.
	// Used by piglet scoping to enumerate available tools.
	GetAllTools func() []ToolInfo

	// GetActiveTools returns the names of currently active tools.
	GetActiveTools func() []string

	// SetActiveTools sets the active tool list by name.
	// Tools not in the list are hidden from the agent.
	SetActiveTools func(names []string)

	// GetFlagValue returns the value of an extension-registered flag.
	GetFlagValue func(name string) any
}

// CancelledResult is the return type for session-mutation commands
// (newSession, fork, navigateTree, switchSession). Upstream returns
// `{ cancelled: boolean }`: Go uses a named struct for clarity.
//
// upstream: types.ts (inline `Promise<{ cancelled: boolean }>` on each method)
type CancelledResult struct {
	Cancelled bool `json:"cancelled"`
}

// NewSessionOptions mirrors upstream's newSession parameter object.
//
// upstream: types.ts:334-338 (ExtensionCommandContext.newSession options)
type NewSessionOptions struct {
	ParentSession string
}

// ForkOptions mirrors upstream's fork options.
//
// upstream: types.ts:342-343
type ForkOptions struct {
	Position string // "before" | "at"
}

// NavigateTreeOptions mirrors upstream's navigateTree options.
//
// upstream: types.ts:347-351
type NavigateTreeOptions struct {
	Summarize           bool   `json:"summarize,omitempty"`
	CustomInstructions  string `json:"customInstructions,omitempty"`
	ReplaceInstructions bool   `json:"replaceInstructions,omitempty"`
	Label               string `json:"label,omitempty"`
}

// SwitchSessionOptions mirrors upstream's switchSession options.
//
// upstream: types.ts:354-356
type SwitchSessionOptions struct{}

// CommandActions is the host-side injection of command-specific
// callbacks. These are the extra surfaces available only in command
// handlers (via [CommandContext]), not in event handlers.
//
// Mirrors upstream ExtensionCommandContextActions (types.ts:1484-1506).
// All fields are optional; nil functions produce safe no-ops.
//
// upstream: types.ts:1484-1506
type CommandActions struct {
	// WaitForIdle blocks until the agent finishes streaming.
	WaitForIdle        func() error
	WaitForIdleContext func(context.Context) error

	// NewSession starts a new session.
	NewSession        func(opts *NewSessionOptions) (CancelledResult, error)
	NewSessionContext func(context.Context, *NewSessionOptions) (CancelledResult, error)

	// Fork creates a new branch from entryId.
	Fork        func(entryID string, opts *ForkOptions) (CancelledResult, error)
	ForkContext func(context.Context, string, *ForkOptions) (CancelledResult, error)

	// NavigateTree moves to a different point in the session tree.
	NavigateTree        func(targetID string, opts *NavigateTreeOptions) (CancelledResult, error)
	NavigateTreeContext func(context.Context, string, *NavigateTreeOptions) (CancelledResult, error)

	// SwitchSession switches to a different session file.
	SwitchSession        func(sessionPath string, opts *SwitchSessionOptions) (CancelledResult, error)
	SwitchSessionContext func(context.Context, string, *SwitchSessionOptions) (CancelledResult, error)

	// Reload reloads extensions, skills, prompts, themes.
	Reload        func() error
	ReloadContext func(context.Context) error
}
