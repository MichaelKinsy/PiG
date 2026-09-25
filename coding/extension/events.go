package extension

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// Event payload and result structs.
//
// Order, naming, and field names mirror upstream's
// .upstream/current/packages/coding-agent/src/core/extensions/types.ts.
//
// Field naming rule (idiomatic Go via parity helper):
//   - upstream field "toolCallId" → Go field "ToolCallID" (Id→ID lift)
//   - upstream field "previousModel" → Go field "PreviousModel"
//   - parity helper [parity.CamelToGoField] handles initialism uplift
//     (Id, Url, Api, Json) at word boundaries.
//
// JSON tags carry upstream's exact camelCase. The parity gates in
// tests/upstream-parity/ enforce both.

// ─── Project Trust (upstream types.ts ProjectTrust*) ─────────────────────

// ProjectTrustEventDecision is an extension's trust verdict for a project.
// Values: "yes" | "no" | "undecided". Mirrors upstream ProjectTrustEventDecision.
type ProjectTrustEventDecision string

const (
	ProjectTrustYes       ProjectTrustEventDecision = "yes"
	ProjectTrustNo        ProjectTrustEventDecision = "no"
	ProjectTrustUndecided ProjectTrustEventDecision = "undecided"
)

// ProjectTrustEvent: upstream types.ts ProjectTrustEvent. Fired before
// project-local inputs load so a global/CLI extension can decide trust.
type ProjectTrustEvent struct {
	Type string `json:"type"` // "project_trust"
	Cwd  string `json:"cwd"`
}

// ProjectTrustEventResult: upstream types.ts ProjectTrustEventResult.
// The first handler returning yes/no wins; undecided falls through.
type ProjectTrustEventResult struct {
	Trusted  ProjectTrustEventDecision `json:"trusted"`
	Remember *bool                     `json:"remember,omitempty"`
}

// ─── Resource Events ─────────────────────────────────────────────────────

// ResourcesDiscoverEvent: upstream types.ts ResourcesDiscoverEvent.
// Fired after session_start to allow extensions to provide additional
// resource paths.
type ResourcesDiscoverEvent struct {
	Type   string `json:"type"`
	Cwd    string `json:"cwd"`
	Reason string `json:"reason"` // "startup" | "reload"
}

// ResourcesDiscoverResult: upstream types.ts ResourcesDiscoverResult.
type ResourcesDiscoverResult struct {
	SkillPaths  []string `json:"skillPaths,omitempty"`
	PromptPaths []string `json:"promptPaths,omitempty"`
	ThemePaths  []string `json:"themePaths,omitempty"`
}

// ─── Session Events ──────────────────────────────────────────────────────

// SessionStartEvent: upstream types.ts SessionStartEvent.
type SessionStartEvent struct {
	Type                string `json:"type"`
	Reason              string `json:"reason"` // "startup" | "reload" | "new" | "resume" | "fork"
	PreviousSessionFile string `json:"previousSessionFile,omitempty"`
}

// SessionInfoChangedEvent: upstream types.ts SessionInfoChangedEvent
// (adopted upstream in 0.80.3).
type SessionInfoChangedEvent struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// SessionBeforeSwitchEvent: upstream types.ts SessionBeforeSwitchEvent.
type SessionBeforeSwitchEvent struct {
	Type              string `json:"type"`
	Reason            string `json:"reason"` // "new" | "resume"
	TargetSessionFile string `json:"targetSessionFile,omitempty"`
}

// SessionBeforeSwitchResult: upstream types.ts SessionBeforeSwitchResult.
type SessionBeforeSwitchResult struct {
	Cancel bool `json:"cancel,omitempty"`
}

// SessionBeforeForkEvent: upstream types.ts SessionBeforeForkEvent.
type SessionBeforeForkEvent struct {
	Type     string `json:"type"`
	EntryID  string `json:"entryId"`
	Position string `json:"position"` // "before" | "at"
}

// SessionBeforeForkResult: upstream types.ts SessionBeforeForkResult.
type SessionBeforeForkResult struct {
	Cancel                  bool `json:"cancel,omitempty"`
	SkipConversationRestore bool `json:"skipConversationRestore,omitempty"`
}

// SessionBeforeCompactEvent: upstream types.ts SessionBeforeCompactEvent.
//
// Signal carries the upstream AbortSignal through Go's context.Context.
type SessionBeforeCompactEvent struct {
	Type               string                `json:"type"`
	Preparation        CompactionPreparation `json:"preparation"`
	BranchEntries      []SessionEntry        `json:"branchEntries"`
	CustomInstructions string                `json:"customInstructions,omitempty"`
	// reason/willRetry adopted upstream in 0.80.3.
	Reason    string          `json:"reason"`
	WillRetry bool            `json:"willRetry"`
	Signal    context.Context `json:"-"`
}

// SessionBeforeCompactResult: upstream types.ts SessionBeforeCompactResult.
type SessionBeforeCompactResult struct {
	Cancel     bool             `json:"cancel,omitempty"`
	Compaction CompactionResult `json:"compaction,omitempty"`
}

// SessionCompactEvent: upstream types.ts SessionCompactEvent.
type SessionCompactEvent struct {
	Type            string          `json:"type"`
	CompactionEntry CompactionEntry `json:"compactionEntry"`
	FromExtension   bool            `json:"fromExtension"`
	// reason/willRetry adopted upstream in 0.80.3.
	Reason    string `json:"reason"`
	WillRetry bool   `json:"willRetry"`
}

// SessionCompactFailedEvent: upstream types.ts SessionCompactFailedEvent.
// Fired after context compaction fails or is aborted.
type SessionCompactFailedEvent struct {
	Type string `json:"type"`
	// Reason is what triggered the compaction: "manual" (/compact),
	// "threshold" (the context threshold), or "overflow" (context overflow
	// recovery).
	Reason string `json:"reason"`
	// ErrorMessage is the error text when compaction failed for a
	// non-abort reason.
	ErrorMessage string `json:"errorMessage,omitempty"`
	// Aborted is true when compaction was cancelled or aborted.
	Aborted bool `json:"aborted"`
	// WillRetry is true when the aborted turn would have been retried after
	// this compaction (overflow recovery).
	WillRetry bool `json:"willRetry"`
	// FromExtension is true when the failing compaction content came from a
	// session_before_compact handler.
	FromExtension bool `json:"fromExtension"`
}

// SessionShutdownEvent: upstream types.ts SessionShutdownEvent.
type SessionShutdownEvent struct {
	Type              string `json:"type"`
	Reason            string `json:"reason"` // "quit" | "reload" | "new" | "resume" | "fork"
	TargetSessionFile string `json:"targetSessionFile,omitempty"`
}

// SessionBeforeTreeEvent: upstream types.ts SessionBeforeTreeEvent.
//
// Signal carries the upstream AbortSignal through Go's context.Context.
type SessionBeforeTreeEvent struct {
	Type        string          `json:"type"`
	Preparation TreePreparation `json:"preparation"`
	Signal      context.Context `json:"-"`
}

// SessionBeforeTreeResult: upstream types.ts SessionBeforeTreeResult.
type SessionBeforeTreeResult struct {
	Cancel              bool                            `json:"cancel,omitempty"`
	Summary             *SessionBeforeTreeResultSummary `json:"summary,omitempty"`
	CustomInstructions  string                          `json:"customInstructions,omitempty"`
	ReplaceInstructions bool                            `json:"replaceInstructions,omitempty"`
	Label               string                          `json:"label,omitempty"`
}

// SessionBeforeTreeResultSummary mirrors the inline `summary` object on
// upstream's SessionBeforeTreeResult.
type SessionBeforeTreeResultSummary struct {
	Summary string `json:"summary"`
	Details any    `json:"details,omitempty"`
	// Usage is the pi-ai Usage of the extension's summarization call.
	Usage any `json:"usage,omitempty"`
}

// SessionTreeEvent: upstream types.ts SessionTreeEvent.
//
// NewLeafID and OldLeafID are upstream `string | null` (NOT optional). The
// difference between "null" and "absent" matters for wire-format round-trip
// through session JSONL: upstream always emits the keys, with `null` when
// no leaf. We use *string so json.Marshal emits null on nil and so the
// distinction survives a marshal/unmarshal cycle.
type SessionTreeEvent struct {
	Type          string             `json:"type"`
	NewLeafID     *string            `json:"newLeafId"`
	OldLeafID     *string            `json:"oldLeafId"`
	SummaryEntry  BranchSummaryEntry `json:"summaryEntry,omitempty"`
	FromExtension bool               `json:"fromExtension,omitempty"`
}

// ─── Agent Events ────────────────────────────────────────────────────────

// ContextEvent: upstream types.ts ContextEvent.
type ContextEvent struct {
	Type     string         `json:"type"`
	Messages []AgentMessage `json:"messages"`
}

// ContextWithSystemEvent: upstream types.ts ContextWithSystemEvent. Handlers
// see the full transcript, system messages included, after the context
// handlers; their returned messages are used as returned.
type ContextWithSystemEvent struct {
	Type     string         `json:"type"`
	Messages []AgentMessage `json:"messages"`
}

// ContextEventResult: upstream types.ts ContextEventResult.
type ContextEventResult struct {
	Messages []AgentMessage `json:"messages,omitempty"`
}

// BeforeProviderRequestEvent: upstream types.ts BeforeProviderRequestEvent.
type BeforeProviderRequestEvent struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// BeforeProviderRequestEventResult: upstream type alias for `unknown`.
type BeforeProviderRequestEventResult = any

// AfterProviderResponseEvent: upstream types.ts AfterProviderResponseEvent.
type AfterProviderResponseEvent struct {
	Type    string            `json:"type"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

// ProviderHeaders mirrors upstream pi-ai ProviderHeaders
// (`Record<string, string | null>`): a nil value signals "delete this header".
type ProviderHeaders = map[string]*string

// BeforeProviderHeadersEvent: upstream types.ts BeforeProviderHeadersEvent.
// Handlers mutate Headers in place before the request is sent.
type BeforeProviderHeadersEvent struct {
	Type    string          `json:"type"`
	Headers ProviderHeaders `json:"headers"`
}

// BeforeAgentStartEvent: upstream types.ts BeforeAgentStartEvent.
type BeforeAgentStartEvent struct {
	Type                string                   `json:"type"`
	Prompt              string                   `json:"prompt"`
	Images              []ImageContent           `json:"images,omitempty"`
	SystemPrompt        string                   `json:"systemPrompt"`
	SystemPromptOptions BuildSystemPromptOptions `json:"systemPromptOptions"`
}

// BeforeAgentStartEventResult: upstream types.ts BeforeAgentStartEventResult.
type BeforeAgentStartEventResult struct {
	Message *CustomMessageRef `json:"message,omitempty"`
	// SystemPrompt replaces the run's system prompt when set, including when
	// it is empty; nil leaves it unchanged (upstream `systemPrompt?: string`).
	SystemPrompt *string `json:"systemPrompt,omitempty"`
}

// CustomMessageRef mirrors upstream's
// `Pick<CustomMessage, "customType" | "content" | "display" | "details">`.
type CustomMessageRef struct {
	CustomType string `json:"customType"`
	Content    any    `json:"content"`
	Display    any    `json:"display,omitempty"`
	Details    any    `json:"details,omitempty"`
}

// AgentStartEvent: upstream types.ts AgentStartEvent.
type AgentStartEvent struct {
	Type string `json:"type"`
}

// AgentBeforeSettleEvent: upstream types.ts AgentBeforeSettleEvent. It is
// awaited after retry, recovery, compaction, and queued continuations stop.
type AgentBeforeSettleEvent struct {
	Type string `json:"type"`
	BoundaryState
}

// AgentSettledEvent: upstream types.ts AgentSettledEvent. Fired after an agent
// run has fully settled and no automatic retry, compaction, or queued
// continuation will run.
type AgentSettledEvent struct {
	Type string `json:"type"`
}

// UIPromptKind mirrors upstream types.ts UIPromptKind:
// "select" | "confirm" | "input" | "editor" | "custom".
type UIPromptKind string

const (
	UIPromptKindSelect  UIPromptKind = "select"
	UIPromptKindConfirm UIPromptKind = "confirm"
	UIPromptKindInput   UIPromptKind = "input"
	UIPromptKindEditor  UIPromptKind = "editor"
	UIPromptKindCustom  UIPromptKind = "custom"
)

// UIPromptStartEvent: upstream types.ts UIPromptStartEvent. Fired when Pi
// starts waiting on a blocking user-facing extension UI prompt. Title is
// omitted when the prompt has none (upstream `...(title ? { title } : {})`).
type UIPromptStartEvent struct {
	Type   string       `json:"type"`   // "ui_prompt_start"
	Reason string       `json:"reason"` // "ui_prompt"
	Kind   UIPromptKind `json:"kind"`
	Title  string       `json:"title,omitempty"`
}

// UIPromptEndEvent: upstream types.ts UIPromptEndEvent. Fired when Pi is no
// longer waiting on a blocking user-facing extension UI prompt. Kind and
// Title repeat the outermost prompt's start event.
type UIPromptEndEvent struct {
	Type   string       `json:"type"`   // "ui_prompt_end"
	Reason string       `json:"reason"` // "ui_prompt"
	Kind   UIPromptKind `json:"kind"`
	Title  string       `json:"title,omitempty"`
}

// AgentEndEvent: upstream types.ts AgentEndEvent.
type AgentEndEvent struct {
	Type     string         `json:"type"`
	Messages []AgentMessage `json:"messages"`
}

// AgentActivityOutcome mirrors upstream's completed/aborted/error boundary state.
type AgentActivityOutcome string

const (
	AgentActivityCompleted AgentActivityOutcome = "completed"
	AgentActivityAborted   AgentActivityOutcome = "aborted"
	AgentActivityError     AgentActivityOutcome = "error"
)

// SessionBoundaryDraft is the wire representation of upstream's closed
// SessionBoundaryDraft union. Fields apply according to Type.
type SessionBoundaryDraft struct {
	Type             string          `json:"type"`
	CustomType       string          `json:"customType,omitempty"`
	Data             any             `json:"data,omitempty"`
	Content          any             `json:"content,omitempty"`
	Display          bool            `json:"display,omitempty"`
	Details          any             `json:"details,omitempty"`
	TargetID         string          `json:"targetId,omitempty"`
	Replacement      json.RawMessage `json:"replacement,omitempty"`
	Summary          string          `json:"summary,omitempty"`
	FirstKeptEntryID *string         `json:"firstKeptEntryId,omitempty"`
	Usage            *ai.Usage       `json:"usage,omitempty"`
}

// MarshalJSON preserves each upstream union member's required fields,
// including null firstKeptEntryId and replacement values.
func (d SessionBoundaryDraft) MarshalJSON() ([]byte, error) {
	switch d.Type {
	case "custom":
		return json.Marshal(struct {
			Type       string `json:"type"`
			CustomType string `json:"customType"`
			Data       any    `json:"data,omitempty"`
		}{d.Type, d.CustomType, d.Data})
	case "custom_message":
		return json.Marshal(struct {
			Type       string `json:"type"`
			CustomType string `json:"customType"`
			Content    any    `json:"content"`
			Display    bool   `json:"display"`
			Details    any    `json:"details,omitempty"`
		}{d.Type, d.CustomType, d.Content, d.Display, d.Details})
	case "context_edit":
		replacement := d.Replacement
		if len(replacement) == 0 {
			replacement = json.RawMessage("null")
		}
		return json.Marshal(struct {
			Type        string          `json:"type"`
			TargetID    string          `json:"targetId"`
			Replacement json.RawMessage `json:"replacement"`
		}{d.Type, d.TargetID, replacement})
	case "compaction":
		return json.Marshal(struct {
			Type             string    `json:"type"`
			Summary          string    `json:"summary"`
			FirstKeptEntryID *string   `json:"firstKeptEntryId"`
			Details          any       `json:"details,omitempty"`
			Usage            *ai.Usage `json:"usage,omitempty"`
		}{d.Type, d.Summary, d.FirstKeptEntryID, d.Details, d.Usage})
	default:
		type plain SessionBoundaryDraft
		return json.Marshal(plain(d))
	}
}

// ProjectedSessionEntry is one boundary preview entry and its model-visible messages.
type ProjectedSessionEntry struct {
	SourceEntry any            `json:"sourceEntry"`
	Messages    []AgentMessage `json:"messages"`
}

// BoundaryContextPreview is the recomputed session/model context after the
// currently proposed entries.
type BoundaryContextPreview struct {
	ContextEntries  []ProjectedSessionEntry `json:"contextEntries"`
	ContextMessages []AgentMessage          `json:"contextMessages"`
	LLMMessages     []any                   `json:"llmMessages"`
	PendingMessages []AgentMessage          `json:"pendingMessages"`
	CanContinue     bool                    `json:"canContinue"`
}

// BoundaryState is shared by turn_end and agent_before_settle upstream.
type BoundaryState struct {
	Entries  []SessionBoundaryDraft `json:"entries"`
	Continue bool                   `json:"continue"`
	Context  BoundaryContextPreview `json:"context"`
	Outcome  AgentActivityOutcome   `json:"outcome"`
}

// BoundaryResult chains proposed entries and explicit continuation. Pointer
// fields preserve omitted versus explicit empty/false results.
type BoundaryResult struct {
	Entries  *[]SessionBoundaryDraft `json:"entries,omitempty"`
	Continue *bool                   `json:"continue,omitempty"`
}

// AgentBeforeSettleEventResult mirrors upstream's BoundaryResult alias.
type AgentBeforeSettleEventResult = BoundaryResult

// TurnStartEvent: upstream types.ts TurnStartEvent.
type TurnStartEvent struct {
	Type      string `json:"type"`
	TurnIndex int    `json:"turnIndex"`
	Timestamp int64  `json:"timestamp"`
}

// TurnEndEvent: upstream types.ts TurnEndEvent.
type TurnEndEvent struct {
	Type               string              `json:"type"`
	TurnIndex          int                 `json:"turnIndex"`
	Message            AgentMessage        `json:"message"`
	ToolResults        []ToolResultMessage `json:"toolResults"`
	MessageEntryID     string              `json:"messageEntryId"`
	ToolResultEntryIds []string            `json:"toolResultEntryIds"`
}

// MessageStartEvent: upstream types.ts MessageStartEvent.
type MessageStartEvent struct {
	Type    string       `json:"type"`
	Message AgentMessage `json:"message"`
}

// MessageUpdateEvent: upstream types.ts MessageUpdateEvent.
type MessageUpdateEvent struct {
	Type                  string                `json:"type"`
	Message               AgentMessage          `json:"message"`
	AssistantMessageEvent AssistantMessageEvent `json:"assistantMessageEvent"`
}

// MessageEndEvent: upstream types.ts MessageEndEvent.
type MessageEndEvent struct {
	Type    string       `json:"type"`
	Message AgentMessage `json:"message"`
}

// MessageEndEventResult mirrors upstream message_end handler result.
type MessageEndEventResult struct {
	// Message replaces the finalized message. Replacement must preserve role.
	Message *AgentMessage `json:"message,omitempty"`
}

// ToolExecutionStartEvent: upstream types.ts ToolExecutionStartEvent.
type ToolExecutionStartEvent struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Args       any    `json:"args"`
}

// ToolExecutionUpdateEvent: upstream types.ts ToolExecutionUpdateEvent.
type ToolExecutionUpdateEvent struct {
	Type          string `json:"type"`
	ToolCallID    string `json:"toolCallId"`
	ToolName      string `json:"toolName"`
	Args          any    `json:"args"`
	PartialResult any    `json:"partialResult"`
}

// ToolExecutionEndEvent: upstream types.ts ToolExecutionEndEvent.
type ToolExecutionEndEvent struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Result     any    `json:"result"`
	IsError    bool   `json:"isError"`
}

// ─── Model Events ────────────────────────────────────────────────────────

// ModelSelectSource mirrors upstream "set" | "cycle" | "restore".
type ModelSelectSource = string

const (
	ModelSelectSourceUser    ModelSelectSource = "set"
	ModelSelectSourceCycle   ModelSelectSource = "cycle"
	ModelSelectSourceRestore ModelSelectSource = "restore"
)

// ModelSelectEvent: upstream types.ts ModelSelectEvent.
type ModelSelectEvent struct {
	Type          string            `json:"type"`
	Model         Model             `json:"model"`
	PreviousModel Model             `json:"previousModel,omitempty"`
	Source        ModelSelectSource `json:"source"`
}

// ThinkingLevelSelectEvent: upstream types.ts ThinkingLevelSelectEvent.
type ThinkingLevelSelectEvent struct {
	Type          string `json:"type"`
	Level         string `json:"level"`
	PreviousLevel string `json:"previousLevel"`
}

// ─── User Bash Events ────────────────────────────────────────────────────

// UserBashEvent: upstream types.ts UserBashEvent.
type UserBashEvent struct {
	Type               string `json:"type"`
	Command            string `json:"command"`
	ExcludeFromContext bool   `json:"excludeFromContext"`
	Cwd                string `json:"cwd"`
}

// UserBashEventResult: upstream types.ts UserBashEventResult.
type UserBashEventResult struct {
	Operations BashOperations `json:"operations,omitempty"`
	Result     BashResult     `json:"result,omitempty"`
}

// ─── Input Events ────────────────────────────────────────────────────────

// InputSource mirrors upstream "interactive" | "rpc" | "extension".
type InputSource = string

const (
	InputSourceUser      InputSource = "interactive"
	InputSourceRPC       InputSource = "rpc"
	InputSourceExtension InputSource = "extension"
)

// InputEvent: upstream types.ts InputEvent.
type InputEvent struct {
	Type   string         `json:"type"`
	Text   string         `json:"text"`
	Images []ImageContent `json:"images,omitempty"`
	Source InputSource    `json:"source"`
	// StreamingBehavior indicates context when input arrives during streaming.
	// "steer" = mid-stream steer, "followUp" = queued follow-up.
	// Empty when the agent is idle. Mirrors upstream v0.77.0.
	StreamingBehavior string `json:"streamingBehavior,omitempty"`
}

// InputEventResult is the sealed union of handler responses for the "input"
// extension event. Mirrors upstream `types.ts InputEventResult`:
//
//	export type InputEventResult =
//	    | { action: "continue" }
//	    | { action: "transform"; text: string; images?: ImageContent[] }
//	    | { action: "handled" };
//
// The interface is package-sealed via the unexported `isInputEventResult`
// marker method: only the three variants in this package can satisfy it.
// Custom MarshalJSON/UnmarshalJSON in marshalling.go preserves upstream's
// `{"action": "..."}` discriminator wire shape.
//
// Authors return one of:
//   - [InputEventResultContinue]   (let the agent process the input as-is)
//   - [InputEventResultTransform]  (replace input text/images before send)
//   - [InputEventResultHandled]    (extension fully handled; skip agent)
//
// upstream: types.ts:750
type InputEventResult interface {
	isInputEventResult()
}

// InputEventResultContinue: upstream `{ action: "continue" }`.
type InputEventResultContinue struct{}

func (InputEventResultContinue) isInputEventResult() {}

// InputEventResultTransform: upstream `{ action: "transform"; text: string;
// images?: ImageContent[] }`. Replaces the user input before it reaches
// the agent.
type InputEventResultTransform struct {
	Text   string         `json:"text"`
	Images []ImageContent `json:"images,omitempty"`
}

func (InputEventResultTransform) isInputEventResult() {}

// InputEventResultHandled: upstream `{ action: "handled" }`. Tells the
// host the extension fully handled the input; the agent does NOT run for
// this turn.
type InputEventResultHandled struct{}

func (InputEventResultHandled) isInputEventResult() {}

// ─── Tool Events (call) ──────────────────────────────────────────────────

// ToolCallEventBase mirrors upstream's internal ToolCallEventBase. Embedded
// in each per-tool variant so all variants share the discriminator and
// toolCallId fields.
//
// Go embeds the common fields so reflection sees the same promoted shape.
type ToolCallEventBase struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
}

// BashToolCallEvent: upstream types.ts BashToolCallEvent.
type BashToolCallEvent struct {
	ToolCallEventBase
	ToolName string        `json:"toolName"` // "bash"
	Input    BashToolInput `json:"input"`
}

// PowerShellToolCallEvent: upstream types.ts PowerShellToolCallEvent.
type PowerShellToolCallEvent struct {
	ToolCallEventBase
	ToolName string              `json:"toolName"` // "powershell"
	Input    PowerShellToolInput `json:"input"`
}

// ReadToolCallEvent: upstream types.ts ReadToolCallEvent.
type ReadToolCallEvent struct {
	ToolCallEventBase
	ToolName string        `json:"toolName"` // "read"
	Input    ReadToolInput `json:"input"`
}

// EditToolCallEvent: upstream types.ts EditToolCallEvent.
type EditToolCallEvent struct {
	ToolCallEventBase
	ToolName string        `json:"toolName"` // "edit"
	Input    EditToolInput `json:"input"`
}

// WriteToolCallEvent: upstream types.ts WriteToolCallEvent.
type WriteToolCallEvent struct {
	ToolCallEventBase
	ToolName string         `json:"toolName"` // "write"
	Input    WriteToolInput `json:"input"`
}

// GrepToolCallEvent: upstream types.ts GrepToolCallEvent.
type GrepToolCallEvent struct {
	ToolCallEventBase
	ToolName string        `json:"toolName"` // "grep"
	Input    GrepToolInput `json:"input"`
}

// FindToolCallEvent: upstream types.ts FindToolCallEvent.
type FindToolCallEvent struct {
	ToolCallEventBase
	ToolName string        `json:"toolName"` // "find"
	Input    FindToolInput `json:"input"`
}

// LsToolCallEvent: upstream types.ts LsToolCallEvent.
type LsToolCallEvent struct {
	ToolCallEventBase
	ToolName string      `json:"toolName"` // "ls"
	Input    LsToolInput `json:"input"`
}

// CustomToolCallEvent: upstream types.ts CustomToolCallEvent.
type CustomToolCallEvent struct {
	ToolCallEventBase
	ToolName string         `json:"toolName"`
	Input    map[string]any `json:"input"`
}

// ToolCallEventResult: upstream types.ts ToolCallEventResult.
type ToolCallEventResult struct {
	Block  bool   `json:"block,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Terminate hints that the agent should stop after the current tool batch
	// when this call is blocked. Early termination only happens when every
	// finalized tool result in the batch sets it.
	Terminate bool `json:"terminate,omitempty"`
}

// ─── Tool Events (result) ────────────────────────────────────────────────

// ToolResultEventBase mirrors upstream's internal ToolResultEventBase.
type ToolResultEventBase struct {
	Type       string         `json:"type"`
	ToolCallID string         `json:"toolCallId"`
	Input      map[string]any `json:"input"`
	Content    []any          `json:"content"` // (TextContent | ImageContent)[]
	IsError    bool           `json:"isError"`
	// Usage is the usage of the tool execution itself, if available
	// (upstream `usage?: Usage`).
	Usage any `json:"usage,omitempty"`
}

// ToolDetailsConverter is implemented by a built-in tool's internal result
// details to produce the upstream SDK wire shape for the `details` field of
// a tool_result event. pig keeps richer internal detail structs for the TUI
// renderer; this contract lets the extension emission boundary convert them
// to the lean upstream shapes without the generic runtime depending on the
// concrete tool package.
type ToolDetailsConverter interface {
	ToolResultDetails() any
}

// ToolResultDetailsFor converts a tool result's Details to its SDK wire shape
// when the value implements [ToolDetailsConverter]; other details (custom-tool
// JSON, the already-SDK-shaped edit details, or nil) pass through unchanged.
func ToolResultDetailsFor(details any) any {
	if c, ok := details.(ToolDetailsConverter); ok {
		return c.ToolResultDetails()
	}
	return details
}

// ToolTruncation is the upstream TruncationResult wire shape (truncate.ts:15).
// It is attached as the `truncation` field of read/bash/grep/find/ls tool
// result details when output was truncated. All fields are present in a real
// truncation object (it is only attached when truncation occurred).
type ToolTruncation struct {
	Content               string `json:"content"`
	Truncated             bool   `json:"truncated"`
	TruncatedBy           string `json:"truncatedBy"` // "lines" | "bytes"
	TotalLines            int    `json:"totalLines"`
	TotalBytes            int    `json:"totalBytes"`
	OutputLines           int    `json:"outputLines"`
	OutputBytes           int    `json:"outputBytes"`
	LastLinePartial       bool   `json:"lastLinePartial"`
	FirstLineExceedsLimit bool   `json:"firstLineExceedsLimit"`
	MaxLines              int    `json:"maxLines"`
	MaxBytes              int    `json:"maxBytes"`
}

// BashToolDetails mirrors upstream bash.ts:31. Attached only when output
// was truncated.
type BashToolDetails struct {
	Truncation     *ToolTruncation `json:"truncation,omitempty"`
	FullOutputPath string          `json:"fullOutputPath,omitempty"`
}

// PowerShellToolDetails is the bash details shape (upstream powershell.ts).
type PowerShellToolDetails = BashToolDetails

// ReadToolDetails mirrors upstream read.ts:28. Attached only when output
// was truncated (or the first line exceeded the byte limit).
type ReadToolDetails struct {
	Truncation *ToolTruncation `json:"truncation,omitempty"`
}

// GrepToolDetails mirrors upstream grep.ts:41. Each field is set only when
// its condition holds; the whole object is omitted when empty.
type GrepToolDetails struct {
	Truncation        *ToolTruncation `json:"truncation,omitempty"`
	MatchLimitReached int             `json:"matchLimitReached,omitempty"`
	LinesTruncated    bool            `json:"linesTruncated,omitempty"`
}

// FindToolDetails mirrors upstream find.ts:32.
type FindToolDetails struct {
	Truncation         *ToolTruncation `json:"truncation,omitempty"`
	ResultLimitReached int             `json:"resultLimitReached,omitempty"`
}

// LsToolDetails mirrors upstream ls.ts:23.
type LsToolDetails struct {
	Truncation        *ToolTruncation `json:"truncation,omitempty"`
	EntryLimitReached int             `json:"entryLimitReached,omitempty"`
}

// BashToolResultEvent: upstream types.ts BashToolResultEvent.
type BashToolResultEvent struct {
	ToolResultEventBase
	ToolName string           `json:"toolName"` // "bash"
	Details  *BashToolDetails `json:"details,omitempty"`
}

// PowerShellToolResultEvent: upstream types.ts PowerShellToolResultEvent.
type PowerShellToolResultEvent struct {
	ToolResultEventBase
	ToolName string                 `json:"toolName"` // "powershell"
	Details  *PowerShellToolDetails `json:"details,omitempty"`
}

// ReadToolResultEvent: upstream types.ts ReadToolResultEvent.
type ReadToolResultEvent struct {
	ToolResultEventBase
	ToolName string           `json:"toolName"` // "read"
	Details  *ReadToolDetails `json:"details,omitempty"`
}

// EditToolResultEvent: upstream types.ts EditToolResultEvent.
type EditToolResultEvent struct {
	ToolResultEventBase
	ToolName string           `json:"toolName"` // "edit"
	Details  *EditToolDetails `json:"details,omitempty"`
}

// EditToolDetails is the upstream SDK contract for built-in edit-tool
// result details (edit.ts:61). Extensions and PostToolUse hooks receive
// this shape on tool_result events for the `edit` tool: a display diff,
// a standard unified patch, and the first changed line for navigation.
type EditToolDetails struct {
	Diff             string `json:"diff"`
	Patch            string `json:"patch"`
	FirstChangedLine int    `json:"firstChangedLine,omitempty"`
}

// WriteToolResultEvent: upstream types.ts WriteToolResultEvent.
type WriteToolResultEvent struct {
	ToolResultEventBase
	ToolName string `json:"toolName"` // "write"
	Details  any    `json:"details,omitempty"`
}

// GrepToolResultEvent: upstream types.ts GrepToolResultEvent.
type GrepToolResultEvent struct {
	ToolResultEventBase
	ToolName string           `json:"toolName"` // "grep"
	Details  *GrepToolDetails `json:"details,omitempty"`
}

// FindToolResultEvent: upstream types.ts FindToolResultEvent.
type FindToolResultEvent struct {
	ToolResultEventBase
	ToolName string           `json:"toolName"` // "find"
	Details  *FindToolDetails `json:"details,omitempty"`
}

// LsToolResultEvent: upstream types.ts LsToolResultEvent.
type LsToolResultEvent struct {
	ToolResultEventBase
	ToolName string         `json:"toolName"` // "ls"
	Details  *LsToolDetails `json:"details,omitempty"`
}

// CustomToolResultEvent: upstream types.ts CustomToolResultEvent.
type CustomToolResultEvent struct {
	ToolResultEventBase
	ToolName string `json:"toolName"`
	Details  any    `json:"details,omitempty"`
}

// ToolResultEventResult: upstream types.ts ToolResultEventResult.
type ToolResultEventResult struct {
	Content []any `json:"content,omitempty"` // (TextContent | ImageContent)[]
	Details any   `json:"details,omitempty"`
	// IsError is nil when the handler leaves the error flag unchanged, as
	// upstream's optional `isError?: boolean` is undefined.
	IsError *bool `json:"isError,omitempty"`
	// Usage mirrors upstream ToolResultEventResult.usage (pi-ai Usage). Carried
	// untyped over the extension wire boundary, consistent with Details above and
	// the AgentMessage alias; folded into session usage totals by the
	// deferred-tools accounting path.
	Usage any `json:"usage,omitempty"`
}

// ─── Union event aliases ──────────────────────────────────────────────────

// ToolCallEvent is the sealed union of per-tool ToolCallEvent variants.
// Mirrors upstream's `export type ToolCallEvent = | BashToolCallEvent |
// PowerShellToolCallEvent | ReadToolCallEvent | EditToolCallEvent | WriteToolCallEvent |
// GrepToolCallEvent | FindToolCallEvent | LsToolCallEvent |
// CustomToolCallEvent` (types.ts:810).
//
// Package-sealed via the unexported `isToolCallEvent` marker method: only
// the nine variant structs declared in this package can satisfy it.
// Authors handle the union via a type switch on the concrete variant.
// Custom UnmarshalJSON in marshalling.go probes the `toolName` field to
// dispatch to the correct variant when reading session JSONL.
//
// upstream: types.ts:810
type ToolCallEvent interface {
	isToolCallEvent()
}

func (BashToolCallEvent) isToolCallEvent()       {}
func (PowerShellToolCallEvent) isToolCallEvent() {}
func (ReadToolCallEvent) isToolCallEvent()       {}
func (EditToolCallEvent) isToolCallEvent()       {}
func (WriteToolCallEvent) isToolCallEvent()      {}
func (GrepToolCallEvent) isToolCallEvent()       {}
func (FindToolCallEvent) isToolCallEvent()       {}
func (LsToolCallEvent) isToolCallEvent()         {}
func (CustomToolCallEvent) isToolCallEvent()     {}

// ToolResultEvent is the sealed union of per-tool ToolResultEvent variants.
// Mirrors upstream's `export type ToolResultEvent = | BashToolResultEvent |
// PowerShellToolResultEvent | ... | CustomToolResultEvent` (types.ts:869).
//
// Package-sealed via the unexported `isToolResultEvent` marker method.
// Custom UnmarshalJSON in marshalling.go dispatches by `toolName`.
//
// upstream: types.ts:869
type ToolResultEvent interface {
	isToolResultEvent()
}

func (BashToolResultEvent) isToolResultEvent()       {}
func (PowerShellToolResultEvent) isToolResultEvent() {}
func (ReadToolResultEvent) isToolResultEvent()       {}
func (EditToolResultEvent) isToolResultEvent()       {}
func (WriteToolResultEvent) isToolResultEvent()      {}
func (GrepToolResultEvent) isToolResultEvent()       {}
func (FindToolResultEvent) isToolResultEvent()       {}
func (LsToolResultEvent) isToolResultEvent()         {}
func (CustomToolResultEvent) isToolResultEvent()     {}
