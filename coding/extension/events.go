package extension

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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
// test/upstream-parity/ enforce both.

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
	Cancel bool `json:"cancel,omitempty"`
	// Compaction is the extension-supplied CompactionResult (types.ts:1472); a subprocess extension's raw JSON result reaches the host undecoded.
	Compaction *CompactionResult `json:"compaction,omitempty"`
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

// SessionBeforeTreeResult: upstream types.ts SessionBeforeTreeResult. A nil pointer is a field the handler left undefined, so the navigation keeps its own value; an explicit false or empty string overrides it.
type SessionBeforeTreeResult struct {
	Cancel              bool                            `json:"cancel,omitempty"`
	Summary             *SessionBeforeTreeResultSummary `json:"summary,omitempty"`
	CustomInstructions  *string                         `json:"customInstructions,omitempty"`
	ReplaceInstructions *bool                           `json:"replaceInstructions,omitempty"`
	Label               *string                         `json:"label,omitempty"`
}

// SessionBeforeTreeResultSummary mirrors the inline `summary` object on
// upstream's SessionBeforeTreeResult.
type SessionBeforeTreeResultSummary struct {
	Summary string `json:"summary"`
	Details any    `json:"details,omitempty"`
	// Usage is the pi-ai Usage of the extension's summarization call.
	Usage *ai.Usage `json:"usage,omitempty"`
}

// UnmarshalJSON keeps the member order of the `details` object the extension wrote.
func (s *SessionBeforeTreeResultSummary) UnmarshalJSON(data []byte) error {
	type plain SessionBeforeTreeResultSummary
	return orderedjson.UnmarshalFields(data, (*plain)(s), "details")
}

// SessionTreeEvent: upstream types.ts SessionTreeEvent.
//
// NewLeafID and OldLeafID are upstream `string | null` (NOT optional). The
// difference between "null" and "absent" matters for wire-format round-trip
// through session JSONL: upstream always emits the keys, with `null` when
// no leaf. We use *string so json.Marshal emits null on nil and so the
// distinction survives a marshal/unmarshal cycle.
type SessionTreeEvent struct {
	Type          string              `json:"type"`
	NewLeafID     *string             `json:"newLeafId"`
	OldLeafID     *string             `json:"oldLeafId"`
	SummaryEntry  *BranchSummaryEntry `json:"summaryEntry,omitempty"`
	FromExtension bool                `json:"fromExtension,omitempty"`
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

// ProviderStreamEvent: upstream types.ts ProviderStreamEvent. Fired for a parsed
// provider stream event before it is normalized. Data is adapter-owned and
// read-only.
//
// upstream: types.ts:884-890
type ProviderStreamEvent struct {
	Type     string `json:"type"`
	Provider string `json:"provider"`
	API      string `json:"api"`
	Model    string `json:"model"`
	Data     any    `json:"data"`
}

// McpServersChangeEvent: upstream types.ts McpServersChangeEvent. Fired when an
// extension registers or unregisters an MCP server after the extensions are
// bound (see [API.RegisterMcpServer]). Servers registered while extensions load
// are read with [API.GetMcpServers] on session_start. Handling this event marks
// an extension as the one that connects registered servers.
//
// upstream: types.ts:699-709
type McpServersChangeEvent struct {
	Type string `json:"type"`
	// Servers is every registered server after the change.
	Servers []RegisteredMcpServer `json:"servers"`
}

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
	Type         string         `json:"type"`
	Prompt       string         `json:"prompt"`
	Images       []ImageContent `json:"images,omitempty"`
	SystemPrompt string         `json:"systemPrompt"`
	// SystemPromptOptions is the per-handler value view of the run's options. Use BeforeAgentStartOptions with the dispatch context to replace collections on the shared per-run object.
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
	Display    bool   `json:"display"`
	Details    any    `json:"details,omitempty"`
}

// UnmarshalJSON keeps the member order of the `details` object the extension wrote.
func (m *CustomMessageRef) UnmarshalJSON(data []byte) error {
	type plain CustomMessageRef
	return orderedjson.UnmarshalFields(data, (*plain)(m), "details")
}

// AgentStartEvent: upstream types.ts AgentStartEvent.
type AgentStartEvent struct {
	Type string `json:"type"`
}

// BoundaryBaseEvent is the closed input union of actionable turn_end and agent_before_settle events. The runner supplies each handler's proposal and preview.
type BoundaryBaseEvent interface{ boundaryBaseEvent() }

func (TurnEndEvent) boundaryBaseEvent()            {}
func (*AgentBeforeSettleEvent) boundaryBaseEvent() {}

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
	// Aborted is whether the run ended because it was aborted, for example with Escape.
	Aborted bool `json:"aborted"`
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

// SessionBoundaryDraftType is the discriminator of the SessionBoundaryDraft union: the type of each upstream member.
type SessionBoundaryDraftType string

const (
	// SessionBoundaryDraftCustom is upstream CustomEntryDraft.
	SessionBoundaryDraftCustom SessionBoundaryDraftType = "custom"
	// SessionBoundaryDraftCustomMessage is upstream CustomMessageEntryDraft.
	SessionBoundaryDraftCustomMessage SessionBoundaryDraftType = "custom_message"
	// SessionBoundaryDraftContextEdit is upstream ContextEditEntryDraft.
	SessionBoundaryDraftContextEdit SessionBoundaryDraftType = "context_edit"
	// SessionBoundaryDraftCompaction is upstream CompactionEntryDraft.
	SessionBoundaryDraftCompaction SessionBoundaryDraftType = "compaction"
)

// SessionBoundaryDraft is the wire representation of upstream's closed
// SessionBoundaryDraft union. Fields apply according to Type.
type SessionBoundaryDraft struct {
	Type             SessionBoundaryDraftType `json:"type"`
	CustomType       string                   `json:"customType,omitempty"`
	Data             any                      `json:"data,omitempty"`
	Content          any                      `json:"content,omitempty"`
	Display          bool                     `json:"display,omitempty"`
	Details          any                      `json:"details,omitempty"`
	TargetID         string                   `json:"targetId,omitempty"`
	Replacement      json.RawMessage          `json:"replacement,omitempty"`
	Summary          string                   `json:"summary,omitempty"`
	FirstKeptEntryID *string                  `json:"firstKeptEntryId,omitempty"`
	Usage            *ai.Usage                `json:"usage,omitempty"`
}

// MarshalJSON preserves each upstream union member's required fields,
// including null firstKeptEntryId and replacement values.
func (d SessionBoundaryDraft) MarshalJSON() ([]byte, error) {
	switch d.Type {
	case SessionBoundaryDraftCustom:
		return json.Marshal(struct {
			Type       SessionBoundaryDraftType `json:"type"`
			CustomType string                   `json:"customType"`
			Data       any                      `json:"data,omitempty"`
		}{d.Type, d.CustomType, d.Data})
	case SessionBoundaryDraftCustomMessage:
		return json.Marshal(struct {
			Type       SessionBoundaryDraftType `json:"type"`
			CustomType string                   `json:"customType"`
			Content    any                      `json:"content"`
			Display    bool                     `json:"display"`
			Details    any                      `json:"details,omitempty"`
		}{d.Type, d.CustomType, d.Content, d.Display, d.Details})
	case SessionBoundaryDraftContextEdit:
		replacement := d.Replacement
		if len(replacement) == 0 {
			replacement = json.RawMessage("null")
		}
		return json.Marshal(struct {
			Type        SessionBoundaryDraftType `json:"type"`
			TargetID    string                   `json:"targetId"`
			Replacement json.RawMessage          `json:"replacement"`
		}{d.Type, d.TargetID, replacement})
	case SessionBoundaryDraftCompaction:
		return json.Marshal(struct {
			Type             SessionBoundaryDraftType `json:"type"`
			Summary          string                   `json:"summary"`
			FirstKeptEntryID *string                  `json:"firstKeptEntryId"`
			Details          any                      `json:"details,omitempty"`
			Usage            *ai.Usage                `json:"usage,omitempty"`
		}{d.Type, d.Summary, d.FirstKeptEntryID, d.Details, d.Usage})
	default:
		type plain SessionBoundaryDraft
		return json.Marshal(plain(d))
	}
}

// UnmarshalJSON keeps the member order of the `data` and `details` objects the extension wrote.
func (d *SessionBoundaryDraft) UnmarshalJSON(data []byte) error {
	type plain SessionBoundaryDraft
	return orderedjson.UnmarshalFields(data, (*plain)(d), "data", "details")
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
	LLMMessages     []ai.Message            `json:"llmMessages"`
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

// TurnEndEventResult mirrors upstream's BoundaryResult alias for turn_end handlers.
type TurnEndEventResult = BoundaryResult

// AgentBeforeSettleEventResult mirrors upstream's BoundaryResult alias.
type AgentBeforeSettleEventResult = BoundaryResult

// TurnStartEvent: upstream types.ts TurnStartEvent.
type TurnStartEvent struct {
	Type      string `json:"type"`
	TurnIndex int    `json:"turnIndex"`
	Timestamp int64  `json:"timestamp"`
}

// TurnEndEvent carries the completed turn and the actionable boundary proposal and preview (upstream types.ts TurnEndEvent).
type TurnEndEvent struct {
	*BoundaryState
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
	// WireArgs is the JSON the event carries for Args on the extension wire: the model's arguments in the order it wrote them, which the map in Args cannot keep. Nil when Args marshals as it is.
	WireArgs json.RawMessage `json:"-"`
	// ParentToolCallID is set when another tool (for example a codemode script)
	// made this call. upstream: parentToolCallId?: string
	ParentToolCallID string `json:"parentToolCallId,omitempty"`
}

// MarshalJSON writes the event with WireArgs in place of Args, in Pi's member order.
func (e ToolExecutionStartEvent) MarshalJSON() ([]byte, error) {
	type plain ToolExecutionStartEvent
	if e.WireArgs != nil {
		e.Args = e.WireArgs
	}
	return json.Marshal(plain(e))
}

// ToolExecutionUpdateEvent: upstream types.ts ToolExecutionUpdateEvent.
type ToolExecutionUpdateEvent struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Args       any    `json:"args"`
	// WireArgs is the JSON the event carries for Args on the extension wire; see [ToolExecutionStartEvent.WireArgs].
	WireArgs      json.RawMessage `json:"-"`
	PartialResult any             `json:"partialResult"`
	// WirePartialResult is the value the event carries for PartialResult on the extension wire, with its members in the order the tool wrote them. Nil when PartialResult marshals as it is.
	WirePartialResult any `json:"-"`
	// ParentToolCallID is set when another tool (for example a codemode script)
	// made this call. upstream: parentToolCallId?: string
	ParentToolCallID string `json:"parentToolCallId,omitempty"`
}

// MarshalJSON writes the event with the wire values in place of Args and PartialResult, in Pi's member order.
func (e ToolExecutionUpdateEvent) MarshalJSON() ([]byte, error) {
	type plain ToolExecutionUpdateEvent
	if e.WireArgs != nil {
		e.Args = e.WireArgs
	}
	if e.WirePartialResult != nil {
		e.PartialResult = e.WirePartialResult
	}
	return json.Marshal(plain(e))
}

// ToolExecutionEndEvent: upstream types.ts ToolExecutionEndEvent.
type ToolExecutionEndEvent struct {
	Type       string `json:"type"`
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Result     any    `json:"result"`
	// WireResult is the value the event carries for Result on the extension wire, with its members in the order the tool wrote them: a map in Result sorts them. Nil when Result marshals as it is.
	WireResult any  `json:"-"`
	IsError    bool `json:"isError"`
	// DurationMs is the milliseconds execute() took, measured with a monotonic clock; absent when the tool did not run.
	// upstream: types.ts ToolExecutionEndEvent.durationMs
	DurationMs *int64 `json:"durationMs,omitempty"`
	// ParentToolCallID is set when another tool (for example a codemode script)
	// made this call. upstream: parentToolCallId?: string
	ParentToolCallID string `json:"parentToolCallId,omitempty"`
}

// MarshalJSON writes the event with WireResult in place of Result, in Pi's member order.
func (e ToolExecutionEndEvent) MarshalJSON() ([]byte, error) {
	type plain ToolExecutionEndEvent
	if e.WireResult != nil {
		e.Result = e.WireResult
	}
	return json.Marshal(plain(e))
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
	Type          string                `json:"type"`
	Level         ai.ModelThinkingLevel `json:"level"`
	PreviousLevel ai.ModelThinkingLevel `json:"previousLevel"`
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
	Type string `json:"type"`
	// ToolCallID is the call's id. For calls another tool made (with
	// ParentToolCallID set), pi assigns `<parent id>/<n>`; such ids never appear
	// as tool calls or tool results in the transcript, only in the parent
	// result's `nestedCalls` record.
	ToolCallID string `json:"toolCallId"`
	// ParentToolCallID is set when another tool (for example a codemode script)
	// issued this call. upstream: parentToolCallId?: string
	ParentToolCallID string `json:"parentToolCallId,omitempty"`
	// WireInput points at the call's input as JSON in the member order the model wrote it, edited as JavaScript edits an object: a retained member keeps its place and a new member follows. A Go map has no order, so [CustomToolCallEvent.Input] carries the values and this carries the order. The Session seeds it from the call's arguments, and a subprocess extension's reply replaces the bytes it points at, so every copy of the event shares the edit. Nil when Input is the only source.
	WireInput *json.RawMessage `json:"-"`
}

// CustomToolCallEvent: upstream types.ts CustomToolCallEvent. It also stands for the nine upstream per-tool variants (Bash, PowerShell, Read, Edit, Write, Grep, Find and Ls ToolCallEvent), which differ from it only in the compile-time type of `input`; the Session emits this type for every tool.
type CustomToolCallEvent struct {
	ToolCallEventBase
	ToolName string         `json:"toolName"`
	Input    map[string]any `json:"input"`
}

// IsToolCallEventType reports whether event is a tool call of the named tool. It narrows nothing: a caller that needs the variant type-asserts after the test, as upstream's type guard narrows the event.
// Mirrors upstream types.ts isToolCallEventType, which compares event.toolName with toolName.
func IsToolCallEventType(toolName string, event ToolCallEvent) bool {
	if event, ok := event.(CustomToolCallEvent); ok {
		return event.ToolName == toolName
	}
	return false
}

// toolResultName is the toolName of a tool_result event, whichever union member carries it.
func toolResultName(event ToolResultEvent) (string, bool) {
	switch event := event.(type) {
	case BashToolResultEvent:
		return event.ToolName, true
	case PowerShellToolResultEvent:
		return event.ToolName, true
	case ReadToolResultEvent:
		return event.ToolName, true
	case EditToolResultEvent:
		return event.ToolName, true
	case WriteToolResultEvent:
		return event.ToolName, true
	case GrepToolResultEvent:
		return event.ToolName, true
	case FindToolResultEvent:
		return event.ToolName, true
	case LsToolResultEvent:
		return event.ToolName, true
	case CustomToolResultEvent:
		return event.ToolName, true
	}
	return "", false
}

// IsBashToolResult reports whether event is the result of the bash tool. upstream: types.ts:1315 isBashToolResult, e.toolName === "bash".
// It narrows nothing: the Session emits CustomToolResultEvent for every tool, and a caller that needs a variant type-asserts after the test.
func IsBashToolResult(event ToolResultEvent) bool { return toolResultNamed(event, "bash") }

// IsPowerShellToolResult reports whether event is the result of the powershell tool. upstream: types.ts:1318 isPowerShellToolResult.
func IsPowerShellToolResult(event ToolResultEvent) bool { return toolResultNamed(event, "powershell") }

// IsReadToolResult reports whether event is the result of the read tool. upstream: types.ts:1321 isReadToolResult.
func IsReadToolResult(event ToolResultEvent) bool { return toolResultNamed(event, "read") }

// IsEditToolResult reports whether event is the result of the edit tool. upstream: types.ts:1324 isEditToolResult.
func IsEditToolResult(event ToolResultEvent) bool { return toolResultNamed(event, "edit") }

// IsWriteToolResult reports whether event is the result of the write tool. upstream: types.ts:1327 isWriteToolResult.
func IsWriteToolResult(event ToolResultEvent) bool { return toolResultNamed(event, "write") }

// IsGrepToolResult reports whether event is the result of the grep tool. upstream: types.ts:1330 isGrepToolResult.
func IsGrepToolResult(event ToolResultEvent) bool { return toolResultNamed(event, "grep") }

// IsFindToolResult reports whether event is the result of the find tool. upstream: types.ts:1333 isFindToolResult.
func IsFindToolResult(event ToolResultEvent) bool { return toolResultNamed(event, "find") }

// IsLsToolResult reports whether event is the result of the ls tool. upstream: types.ts:1336 isLsToolResult.
func IsLsToolResult(event ToolResultEvent) bool { return toolResultNamed(event, "ls") }

func toolResultNamed(event ToolResultEvent, name string) bool {
	got, ok := toolResultName(event)
	return ok && got == name
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
	Type string `json:"type"`
	// ToolCallID is the call's id; `<parent id>/<n>` for nested calls, see [ToolCallEventBase].
	ToolCallID string `json:"toolCallId"`
	// ParentToolCallID is set when another tool (for example a codemode script)
	// issued this call. upstream: parentToolCallId?: string
	ParentToolCallID string         `json:"parentToolCallId,omitempty"`
	Input            map[string]any `json:"input"`
	// WireInput is the tool call's arguments as the model wrote them, which a subprocess extension receives as `input` in that member order; a map in Input sorts them. Empty when Input is the only source.
	WireInput json.RawMessage `json:"-"`
	Content   []any           `json:"content"` // (TextContent | ImageContent)[]
	// StructuredContent is the machine-readable result of a tool that declares an
	// outputSchema. Handlers that redact Content should also replace this;
	// replacing Content alone drops it. upstream: structuredContent?: JsonValue
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError"`
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

// TruncationResult is the upstream TruncationResult wire shape (truncate.ts:15).
// It is attached as the `truncation` field of read/bash/grep/find/ls tool
// result details when output was truncated. All fields are present in a real
// truncation object (it is only attached when truncation occurred).
type TruncationResult struct {
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
	Truncation     *TruncationResult `json:"truncation,omitempty"`
	FullOutputPath string            `json:"fullOutputPath,omitempty"`
}

// PowerShellToolDetails is the bash details shape (upstream powershell.ts).
type PowerShellToolDetails = BashToolDetails

// ReadToolDetails mirrors upstream read.ts:28. Attached only when output
// was truncated (or the first line exceeded the byte limit).
type ReadToolDetails struct {
	Truncation *TruncationResult `json:"truncation,omitempty"`
}

// GrepToolDetails mirrors upstream grep.ts:41. Sparse fields retain the requested numeric limit without rounding.
type GrepToolDetails struct {
	Truncation        *TruncationResult `json:"truncation,omitempty"`
	MatchLimitReached float64           `json:"matchLimitReached,omitempty"`
	LinesTruncated    bool              `json:"linesTruncated,omitempty"`
}

// FindToolDetails mirrors upstream find.ts:32.
type FindToolDetails struct {
	Truncation         *TruncationResult `json:"truncation,omitempty"`
	ResultLimitReached *float64          `json:"resultLimitReached,omitempty"`
}

// LsToolDetails mirrors upstream ls.ts:23, including fractional requested limits.
type LsToolDetails struct {
	Truncation        *TruncationResult `json:"truncation,omitempty"`
	EntryLimitReached float64           `json:"entryLimitReached,omitempty"`
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

// ToolResultEventResult: upstream types.ts ToolResultEventResult. Omitted fields
// stay as they are, except that replacing Content without returning
// StructuredContent drops the structured content, because it may no longer
// match. Return it along with Content to keep it.
type ToolResultEventResult struct {
	Content []any `json:"content,omitempty"` // (TextContent | ImageContent)[]
	Details any   `json:"details,omitempty"`
	// StructuredContent replaces the result's structured content. upstream: structuredContent?: JsonValue
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	// IsError is nil when the handler leaves the error flag unchanged, as
	// upstream's optional `isError?: boolean` is undefined.
	IsError *bool `json:"isError,omitempty"`
	// Usage replaces the tool's own usage in the session usage totals; nil keeps it.
	// upstream: ToolResultEventResult.usage?: Usage
	Usage *ai.Usage `json:"usage,omitempty"`
}

// UnmarshalJSON keeps the member order of the `details` object the handler wrote.
func (r *ToolResultEventResult) UnmarshalJSON(data []byte) error {
	type plain ToolResultEventResult
	return orderedjson.UnmarshalFields(data, (*plain)(r), "details")
}

// ─── Union event aliases ──────────────────────────────────────────────────

// ToolCallEvent is the event a tool_call handler receives. Pi's `ToolCallEvent` union (types.ts:810) has one member per built-in tool plus CustomToolCallEvent; the variants differ only in the compile-time type of `input`, and Pi's runtime emits the same object shape for every tool. The Session emits [CustomToolCallEvent] for every tool, so it is the one Go representation: a handler reads `ToolName` and the `Input` map. Package-sealed through the unexported `isToolCallEvent` marker.
//
// upstream: types.ts:810
type ToolCallEvent interface {
	ExtensionEvent
	isToolCallEvent()
}

func (CustomToolCallEvent) isToolCallEvent() {}

// ToolResultEvent is the sealed union of per-tool ToolResultEvent variants.
// Mirrors upstream's `export type ToolResultEvent = | BashToolResultEvent |
// PowerShellToolResultEvent | ... | CustomToolResultEvent` (types.ts:869).
//
// Package-sealed via the unexported `isToolResultEvent` marker method.
// Custom UnmarshalJSON in marshalling.go dispatches by `toolName`.
//
// upstream: types.ts:869
type ToolResultEvent interface {
	ExtensionEvent
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
