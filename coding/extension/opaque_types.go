package extension

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/compactiontypes"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
	"github.com/MichaelKinsy/PiG/tui"
)

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
type AgentMessage = agent.AgentMessage

// AgentToolResult mirrors @earendil-works/pi-agent-core AgentToolResult<TDetails>.
type AgentToolResult = agent.AgentToolResult

// AgentToolUpdateCallback mirrors AgentToolUpdateCallback<TDetails>.
type AgentToolUpdateCallback = agent.ToolUpdateCallback

// ThinkingLevel mirrors @earendil-works/pi-agent-core ThinkingLevel.
type ThinkingLevel = agent.ThinkingLevel

// ToolExecutionMode mirrors ToolExecutionMode ("sequential" | "parallel").
type ToolExecutionMode = string

// ─── pi-ai ────────────────────────────────────────────────────────────────

// Model mirrors @earendil-works/pi-ai Model<Api>.
type Model = *ai.Model

// ImageContent mirrors @earendil-works/pi-ai ImageContent.
type ImageContent = ai.ImageContent

// TextContent mirrors @earendil-works/pi-ai TextContent.
type TextContent = any

// AssistantMessageEvent mirrors @earendil-works/pi-ai AssistantMessageEvent.
type AssistantMessageEvent = any

// ToolResultMessage mirrors @earendil-works/pi-ai ToolResultMessage.
type ToolResultMessage = agent.ToolResultMessage

// OAuthCredentials mirrors @earendil-works/pi-ai OAuthCredentials.
type OAuthCredentials = ai.OAuthCredentials

// OAuthLoginCallbacks mirrors @earendil-works/pi-ai OAuthLoginCallbacks.
type OAuthLoginCallbacks = ai.OAuthLoginCallbacks

// SimpleStreamOptions mirrors @earendil-works/pi-ai SimpleStreamOptions.
type SimpleStreamOptions = ai.StreamOptions

// AssistantMessageEventStream mirrors AssistantMessageEventStream.
type AssistantMessageEventStream = *ai.AssistantMessageEventStream

// AIContext mirrors @earendil-works/pi-ai Context (the per-call provider
// context, distinct from pig's ExtensionContext).
type AIContext = ai.TranscriptContext

// API is represented by ai.API in concrete provider declarations.

// ─── pi-tui ───────────────────────────────────────────────────────────────

// Component is @earendil-works/pi-tui Component: what a renderer or widget factory returns (a subprocess extension's is a proxy that
// renders the extension's lines over the wire).
type Component = tui.Component

// OverlayHandle mirrors @earendil-works/pi-tui OverlayHandle.
type OverlayHandle = any

// OverlayOptions mirrors @earendil-works/pi-tui OverlayOptions.
type OverlayOptions = any

// TUI mirrors @earendil-works/pi-tui TUI.
type TUI = tui.TUI

// EditorComponent mirrors @earendil-works/pi-tui EditorComponent.
type EditorComponent = tui.EditorComponent

// EditorTheme mirrors @earendil-works/pi-tui EditorTheme.
type EditorTheme = tui.EditorTheme

// ExtensionUIDialogOptions mirrors upstream ExtensionUIDialogOptions. Its `signal` is the dialog call's context.Context (D3), so only the timeout is a field.
type ExtensionUIDialogOptions struct {
	// Timeout is the dialog's auto-dismiss time in milliseconds, nil for none.
	Timeout *float64 `json:"timeout,omitempty"`
}

// WorkingIndicatorOptions mirrors upstream WorkingIndicatorOptions.
type WorkingIndicatorOptions struct {
	// Frames are the animation frames; a pointer to an empty slice hides the indicator, nil keeps the default frames.
	Frames *[]string `json:"frames,omitempty"`
	// IntervalMs is the frame interval in milliseconds, nil for the default.
	IntervalMs *float64 `json:"intervalMs,omitempty"`
}

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

// WidgetPlacement is where an extension widget renders.
// Mirrors upstream WidgetPlacement (core/extensions/types.ts:122).
type WidgetPlacement string

const (
	WidgetPlacementAboveEditor WidgetPlacement = "aboveEditor"
	WidgetPlacementBelowEditor WidgetPlacement = "belowEditor"
)

// ExtensionWidgetOptions mirrors upstream ExtensionWidgetOptions (core/extensions/types.ts:125); a nil pointer is omitted options.
type ExtensionWidgetOptions struct {
	// Placement is where the widget is rendered. Empty means WidgetPlacementAboveEditor.
	Placement WidgetPlacement `json:"placement,omitempty"`
}

// AutocompleteProviderFactory mirrors upstream AutocompleteProviderFactory.
type AutocompleteProviderFactory func(context.Context, *AutocompleteProvider) (*AutocompleteProvider, error)

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
type AutocompleteProvider struct {
	TriggerCharacters           []string
	GetSuggestions              func(context.Context, []string, int, int, bool) (*AutocompleteSuggestions, error)
	ApplyCompletion             func(context.Context, []string, int, int, AutocompleteItem, string) (AutocompleteCompletion, error)
	ShouldTriggerFileCompletion func(context.Context, []string, int, int) (bool, error)
}

// AutocompleteCompletion carries the complete replacement and UTF-16 cursor position returned by a provider.
type AutocompleteCompletion struct {
	Lines      []string `json:"lines"`
	CursorLine int      `json:"cursorLine"`
	CursorCol  int      `json:"cursorCol"`
}

// ─── coding-agent internals ───────────────────────────────────────────────

// Theme mirrors core/modes/interactive/theme.Theme.
type Theme = any

// CompactionPreparation mirrors core/compaction CompactionPreparation.
type CompactionPreparation = compactiontypes.CompactionPreparation

// CompactionResult is the result of a compaction (core/compaction/compaction.ts:105 CompactionResult<T = unknown>).
// Details is the extension-specific data T.
type CompactionResult struct {
	Summary              string    `json:"summary"`
	FirstKeptEntryID     string    `json:"firstKeptEntryId"`
	TokensBefore         int       `json:"tokensBefore"`
	EstimatedTokensAfter *int      `json:"estimatedTokensAfter,omitempty"`
	Usage                *ai.Usage `json:"usage,omitempty"`
	Details              any       `json:"details,omitempty"`
}

// UnmarshalJSON keeps the member order of the `details` object the extension wrote.
func (r *CompactionResult) UnmarshalJSON(data []byte) error {
	type plain CompactionResult
	return orderedjson.UnmarshalFields(data, (*plain)(r), "details")
}

// CompactionEntry mirrors core/session-manager CompactionEntry (types.ts:786 SessionCompactEvent.compactionEntry).
type CompactionEntry = sessionentry.CompactionEntry

// BranchSummaryEntry mirrors core/session-manager BranchSummaryEntry.
type BranchSummaryEntry = sessionentry.BranchSummaryEntry

// SessionEntry mirrors core/session-manager SessionEntry.
type SessionEntry = sessionentry.SessionEntry

// KeybindingsManager mirrors core/keybindings.KeybindingsManager.
type KeybindingsManager = *tui.TUIKeybindingsManager

// BashResult mirrors core/bash-executor.BashResult. A native result must contain exitCode; its nil value represents undefined, not JSON null. A nil optional fullOutputPath is also undefined. Pre-encoded JSON retains its own null values.
type BashResult = any

// ExecOptions configures a shell command execution.
//
// upstream: core/exec.ts ExecOptions
type ExecOptions struct {
	// Timeout in milliseconds. Pi's timeout is a JavaScript number: a positive value starts a timer and anything else starts none.
	Timeout float64 `json:"timeout,omitempty"`
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

// TreePreparation is types.ts TreePreparation: what a session_before_tree handler receives about the navigation. EntriesToSummarize
// holds the SessionEntry values the navigation abandons.
type TreePreparation struct {
	TargetID           string         `json:"targetId"`
	OldLeafID          *string        `json:"oldLeafId"`
	CommonAncestorID   *string        `json:"commonAncestorId"`
	EntriesToSummarize []SessionEntry `json:"entriesToSummarize"`
	UserWantsSummary   bool           `json:"userWantsSummary"`
	// CustomInstructions are the custom summarization instructions.
	CustomInstructions string `json:"customInstructions,omitempty"`
	// ReplaceInstructions makes CustomInstructions replace the default prompt instead of being appended.
	ReplaceInstructions bool `json:"replaceInstructions,omitempty"`
	// Label is the label to attach to the branch summary entry.
	Label string `json:"label,omitempty"`
}

// ─── Per-tool input values ────────────────────────────────────────────────

// These types mirror the tool-specific input values of upstream core/tools.
// Tool result details with concrete wire shapes are defined in events.go.

// BashToolInput is upstream's BashToolInput (bash.ts bashSchema).
type BashToolInput struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// PowerShellToolInput is the bash input shape (upstream powershell.ts).
type PowerShellToolInput = BashToolInput

// ReadToolInput is upstream's ReadToolInput (read.ts readSchema).
type ReadToolInput struct {
	Path   string   `json:"path"`
	Offset *float64 `json:"offset,omitempty"`
	Limit  *float64 `json:"limit,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// EditToolInput is upstream's EditToolInput (edit.ts editSchema).
type EditToolInput struct {
	Path  string                `json:"path"`
	Edits []EditToolInputChange `json:"edits"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// EditToolInputChange is one targeted replacement of an EditToolInput (edit.ts replaceEditSchema).
type EditToolInputChange struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// WriteToolInput is upstream's WriteToolInput (write.ts writeSchema).
type WriteToolInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// GrepToolInput is upstream's GrepToolInput (grep.ts grepSchema).
type GrepToolInput struct {
	Pattern    string   `json:"pattern"`
	Path       *string  `json:"path,omitempty"`
	Glob       *string  `json:"glob,omitempty"`
	IgnoreCase *bool    `json:"ignoreCase,omitempty"`
	Literal    *bool    `json:"literal,omitempty"`
	Context    *float64 `json:"context,omitempty"`
	Limit      *float64 `json:"limit,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// FindToolInput is upstream's FindToolInput (find.ts findSchema).
type FindToolInput struct {
	Pattern string   `json:"pattern"`
	Path    *string  `json:"path,omitempty"`
	Limit   *float64 `json:"limit,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// LsToolInput is upstream's LsToolInput (ls.ts lsSchema).
type LsToolInput struct {
	Path  *string  `json:"path,omitempty"`
	Limit *float64 `json:"limit,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}

// Per-tool result Details structs (BashToolDetails, ReadToolDetails,
// GrepToolDetails, FindToolDetails, LsToolDetails) and the shared
// TruncationResult wire shape are defined in events.go next to the typed
// *ToolResultEvent variants and EditToolDetails.

// ─── Cancellation ─────────────────────────────────────────────────────────

// AbortSignal was the upstream cancellation primitive (DOM AbortSignal).
// Pig uses context.Context for cancellation (see docs/parity/DIVERGENCES.md D3). This
// deprecated alias preserves source compatibility while giving callers the real
// cancellation contract instead of an untyped value.
//
// Deprecated: use context.Context.
type AbortSignal = context.Context

// ─── Source / autocomplete plumbing ───────────────────────────────────────

// SourceInfo mirrors core/source-info.SourceInfo: where a resource, tool or command came from. It is the shared
// coding/source type, so the TUI's Theme can carry it (theme.ts Theme.sourceInfo) without importing this package.
type SourceInfo = source.SourceInfo

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

// SlashCommandInfo mirrors core/slash-commands.SlashCommandInfo, one entry of getCommands: an extension command, prompt template or skill.
type SlashCommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Source is "extension", "prompt" or "skill".
	Source     string     `json:"source"`
	SourceInfo SourceInfo `json:"sourceInfo"`
}

// CustomEntry mirrors core CustomEntry<T>: a session entry appended via
// AppendEntry that does not participate in LLM context. Like CustomMessage,
// the generic payload is carried untyped (the wire boundary is JSON) and
// renderers type-assert as needed.
type CustomEntry = sessionentry.CustomEntry
