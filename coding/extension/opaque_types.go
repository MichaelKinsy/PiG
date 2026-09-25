package extension

import "context"

// Opaque compatibility aliases preserve upstream extension payloads that cross
// the current dynamic JSON and renderer boundaries. They intentionally expose
// no Go fields or methods. Code that needs a stable typed contract must use the
// concrete event and SDK types defined elsewhere in this package.
//
// Upstream definitions live in
// .upstream/current/packages/coding-agent/src/core/extensions/types.ts and its
// imports.

// ─── pi-agent-core ────────────────────────────────────────────────────────

// AgentMessage mirrors @earendil-works/pi-agent-core AgentMessage.
type AgentMessage = any

// AgentToolResult mirrors @earendil-works/pi-agent-core AgentToolResult<TDetails>.
type AgentToolResult = any

// AgentToolUpdateCallback mirrors AgentToolUpdateCallback<TDetails>.
type AgentToolUpdateCallback = any

// ThinkingLevel mirrors @earendil-works/pi-agent-core ThinkingLevel.
type ThinkingLevel = any

// ToolExecutionMode mirrors ToolExecutionMode ("sequential" | "parallel").
type ToolExecutionMode = string

// ─── pi-ai ────────────────────────────────────────────────────────────────

// Model mirrors @earendil-works/pi-ai Model<Api>.
type Model = any

// ImageContent mirrors @earendil-works/pi-ai ImageContent.
type ImageContent = any

// TextContent mirrors @earendil-works/pi-ai TextContent.
type TextContent = any

// AssistantMessageEvent mirrors @earendil-works/pi-ai AssistantMessageEvent.
type AssistantMessageEvent = any

// ToolResultMessage mirrors @earendil-works/pi-ai ToolResultMessage.
type ToolResultMessage = any

// OAuthCredentials mirrors @earendil-works/pi-ai OAuthCredentials.
type OAuthCredentials = any

// OAuthLoginCallbacks mirrors @earendil-works/pi-ai OAuthLoginCallbacks.
type OAuthLoginCallbacks = any

// SimpleStreamOptions mirrors @earendil-works/pi-ai SimpleStreamOptions.
type SimpleStreamOptions = any

// AssistantMessageEventStream mirrors AssistantMessageEventStream.
type AssistantMessageEventStream = any

// AIContext mirrors @earendil-works/pi-ai Context (the per-call provider
// context, distinct from pig's ExtensionContext).
type AIContext = any

// API is represented by ai.API in concrete provider declarations.

// ─── pi-tui ───────────────────────────────────────────────────────────────

// Component mirrors @earendil-works/pi-tui Component. PiG transports rendered
// component state across the subprocess boundary, so the in-process value is
// opaque here.
type Component = any

// OverlayHandle mirrors @earendil-works/pi-tui OverlayHandle.
type OverlayHandle = any

// OverlayOptions mirrors @earendil-works/pi-tui OverlayOptions.
type OverlayOptions = any

// TUI mirrors @earendil-works/pi-tui TUI.
type TUI = any

// EditorComponent mirrors @earendil-works/pi-tui EditorComponent.
type EditorComponent = any

// EditorTheme mirrors @earendil-works/pi-tui EditorTheme.
type EditorTheme = any

// ExtensionUIDialogOptions mirrors upstream ExtensionUIDialogOptions.
type ExtensionUIDialogOptions = any

// WorkingIndicatorOptions mirrors upstream WorkingIndicatorOptions.
type WorkingIndicatorOptions = any

// TerminalInputHandler mirrors upstream `TerminalInputHandler`
// (types.ts:120): raw terminal byte handler for interactive mode.
// Returns a result indicating whether to consume the input.
type TerminalInputHandler = func(data string) TerminalInputResult

// TerminalInputResult is the return value from a TerminalInputHandler.
// Mirrors upstream `{ consume?: boolean; data?: string }`.
// A nil Data leaves the input unchanged; a non-nil Data replaces it for later
// listeners and for normal handling, and an empty replacement drops it.
type TerminalInputResult struct {
	Consume bool    // true → swallow the keystroke, don't process further
	Data    *string // replacement data (nil = no replacement)
}

// RemoteTerminalInputHandler asks a terminal-input listener that runs in
// another process for its verdict on one chunk. It blocks until the verdict
// arrives, the connection fails, or ctx ends. A failure returns the zero
// result, which leaves the input unchanged.
//
// pig additive (D19): upstream's TerminalInputHandler answers synchronously in
// process. A subprocess listener's answer crosses a socket, so the host calls
// this off its input loop.
type RemoteTerminalInputHandler = func(ctx context.Context, data string) TerminalInputResult

// ExtensionWidgetOptions mirrors upstream ExtensionWidgetOptions.
type ExtensionWidgetOptions = any

// AutocompleteProviderFactory mirrors upstream AutocompleteProviderFactory.
type AutocompleteProviderFactory = any

// SetThemeResult mirrors the inline return type of
// `ExtensionUIContext.setTheme` (types.ts:262: `{ success, error? }`).
// Promoted to a named type because the inline TS object literal
// needs a Go-level identifier; field shape matches verbatim.
type SetThemeResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ThemeMeta mirrors the inline return-element type of
// `ExtensionUIContext.getAllThemes` (types.ts:257 -
// `{ name, path | undefined }[]`). Named for the same reason as
// SetThemeResult.
type ThemeMeta struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// AutocompleteProvider mirrors @earendil-works/pi-tui AutocompleteProvider.
type AutocompleteProvider = any

// ─── coding-agent internals ───────────────────────────────────────────────

// Theme mirrors core/modes/interactive/theme.Theme.
type Theme = any

// CompactionPreparation mirrors core/compaction CompactionPreparation.
type CompactionPreparation = any

// CompactionResult mirrors core/compaction CompactionResult.
type CompactionResult = any

// CompactionEntry mirrors core/session-manager CompactionEntry.
type CompactionEntry = any

// BranchSummaryEntry mirrors core/session-manager BranchSummaryEntry.
type BranchSummaryEntry = any

// SessionEntry mirrors core/session-manager SessionEntry.
type SessionEntry = any

// SessionManager mirrors core/session-manager SessionManager.
type SessionManager = any

// ReadonlySessionManager mirrors core/session-manager ReadonlySessionManager.
type ReadonlySessionManager = any

// ModelRegistry mirrors core/model-registry.ModelRegistry.
type ModelRegistry = any

// KeybindingsManager mirrors core/keybindings.KeybindingsManager.
type KeybindingsManager = any

// ReadonlyFooterDataProvider mirrors core/footer-data-provider.
type ReadonlyFooterDataProvider = any

// BashResult mirrors core/bash-executor.BashResult.
type BashResult = any

// ExecOptions configures a shell command execution.
//
// upstream: core/exec.ts ExecOptions
type ExecOptions struct {
	// Timeout in milliseconds. Zero means no timeout.
	Timeout int `json:"timeout,omitempty"`
	// CWD overrides the working directory. Empty uses the extension's CWD.
	CWD string `json:"cwd,omitempty"`
}

// ExecResult is the outcome of a shell command execution.
//
// upstream: core/exec.ts ExecResult
type ExecResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
	Killed bool   `json:"killed"`
}

// TreePreparation mirrors the upstream TreePreparation interface.
type TreePreparation = any

// ─── Per-tool input values ────────────────────────────────────────────────

// These aliases mirror tool-specific input values from upstream core/tools.
// Tool result details with concrete wire shapes are defined in events.go.

type BashToolInput = any

// PowerShellToolInput is the bash input shape (upstream powershell.ts).
type PowerShellToolInput = BashToolInput
type ReadToolInput = any
type EditToolInput = any
type WriteToolInput = any
type GrepToolInput = any
type FindToolInput = any
type LsToolInput = any

// Per-tool result Details structs (BashToolDetails, ReadToolDetails,
// GrepToolDetails, FindToolDetails, LsToolDetails) and the shared
// ToolTruncation wire shape are defined in events.go next to the typed
// *ToolResultEvent variants and EditToolDetails.

// ─── Cancellation ─────────────────────────────────────────────────────────

// AbortSignal was the upstream cancellation primitive (DOM AbortSignal).
// Pig uses context.Context for cancellation (see DIVERGENCES.md D3). This
// deprecated alias preserves source compatibility while giving callers the real
// cancellation contract instead of an untyped value.
//
// Deprecated: use context.Context.
type AbortSignal = context.Context

// ─── Source / autocomplete plumbing ───────────────────────────────────────

// SourceInfo mirrors core/source-info.SourceInfo.
type SourceInfo = any

// AutocompleteItem mirrors @earendil-works/pi-tui AutocompleteItem.
//
// Concrete shape so subprocess autocomplete responses can deserialise
// into a typed value the host can pass to the TUI editor without an
// extra translation step.
type AutocompleteItem struct {
	Value       string `json:"value"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// AutocompleteSuggestions mirrors @earendil-works/pi-tui AutocompleteSuggestions.
type AutocompleteSuggestions struct {
	Items  []AutocompleteItem `json:"items"`
	Prefix string             `json:"prefix"`
}

// AsyncSuggestionSource is the pig-side surface for an extension-supplied
// autocomplete provider. The subprocess bridge installs an implementation
// that fans the query out to an extension process via the
// "autocomplete.suggest" request method; the host editor calls Suggest
// from a goroutine and merges the response into the popup.
//
// pig-specific: upstream installs in-process AutocompleteProvider
// objects directly into a chain. pig cannot run a synchronous chain
// across the subprocess boundary, so the bridge adapts the chain
// pattern to this out-of-band source interface.
type AsyncSuggestionSource interface {
	Suggest(ctx context.Context, lines []string, cursorLine, cursorCol int) *AutocompleteSuggestions
}

// SlashCommandInfo mirrors core/slash-commands.SlashCommandInfo.
type SlashCommandInfo = any

// CustomMessage mirrors core/messages.CustomMessage<T>.
type CustomMessage = any

// CustomEntry mirrors core CustomEntry<T>: a session entry appended via
// AppendEntry that does not participate in LLM context. Like CustomMessage,
// the generic payload is carried untyped (the wire boundary is JSON) and
// renderers type-assert as needed.
type CustomEntry = any
