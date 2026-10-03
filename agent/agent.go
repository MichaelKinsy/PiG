// Package agent provides the core agent loop and message types.
// Mirrors packages/agent of the pinned Pi release.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// ─── Message Types ────────────────────────────────────────────────────────────

// Role constants mirror upstream.
const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleToolResult = "toolResult"
	// Custom roles used by the coding agent layer.
	RoleBashExecution     = "bashExecution"
	RoleCustom            = "custom"
	RoleBranchSummary     = "branchSummary"
	RoleCompactionSummary = "compactionSummary"
)

// UserMessage is a user turn whose content retains the upstream string-or-block-array shape.
type UserMessage struct {
	Role      string         `json:"role"` // "user"
	Content   ai.UserContent `json:"content"`
	Timestamp int64          `json:"timestamp"`
}

// AssistantMessage is a streamed response from the LLM. Streaming events retain a shallow provider view; Observe and JSON serialization read its current nested state without mutating these exported fields.
type AssistantMessage struct {
	Role                  string                     `json:"role"` // "assistant"
	Content               []ai.AssistantContentBlock `json:"content"`
	Thinking              string                     `json:"-"`
	Timestamp             int64                      `json:"timestamp"`
	Usage                 *ai.Usage                  `json:"usage,omitempty"`
	API                   ai.API                     `json:"api,omitempty"`
	Provider              string                     `json:"provider,omitempty"`
	ModelID               string                     `json:"model,omitempty"`
	ResponseModel         string                     `json:"responseModel,omitempty"`
	ResponseID            string                     `json:"responseId,omitempty"`
	ProviderThinkingLevel string                     `json:"providerThinkingLevel,omitempty"`
	// ThinkingLevel is the level the agent loop requested for this response; see ai.AssistantMessage.ThinkingLevel.
	ThinkingLevel ai.ModelThinkingLevel           `json:"thinkingLevel,omitempty"`
	Diagnostics   []ai.AssistantMessageDiagnostic `json:"diagnostics,omitempty"`
	Deferred      *ai.DeferredHandle              `json:"deferred,omitempty"`
	// StopReason records why the assistant turn ended. Mirrors upstream
	// AssistantMessage.stopReason in packages/ai/src/types.ts.
	// Values: "stop" (normal), "toolUse" (tool calls present),
	//         "aborted" (context cancelled), "error" (EventError received).
	StopReason ai.StopReason `json:"stopReason,omitempty"`
	// ErrorMessage holds the error string when StopReason == "error".
	ErrorMessage  string `json:"errorMessage,omitempty"`
	RawStopReason string `json:"rawStopReason,omitempty"`
	EndTurn       *bool  `json:"endTurn,omitempty"`
	// ThinkingSignature is a legacy runtime convenience; durable signatures
	// live on ThinkingContent blocks in the upstream wire shape.
	ThinkingSignature string `json:"-"`

	// streamView pins top-level values and retains the provider's nested object identities.
	streamView *ai.AssistantMessage
}

// ToolResultMessage carries one tool execution result back to the LLM.
// Mirrors upstream pi-ai ToolResultMessage.
type ToolResultMessage struct {
	Role       string                        `json:"role"` // "toolResult"
	ToolCallID string                        `json:"toolCallId"`
	ToolName   string                        `json:"toolName"`
	Content    []ai.ToolResultMessageContent `json:"content"`
	// Details is the tool's details value. Nil is no details unless DetailsNull is set.
	Details any `json:"details,omitempty"`
	// DetailsNull marks a nil Details as an explicit JSON null: the tool wrote details: null, and JSON writes
	// `"details":null`. Without it a nil Details is absent and JSON omits the key. It has no effect when Details is set.
	DetailsNull bool      `json:"-"`
	Usage       *ai.Usage `json:"usage,omitempty"`
	// NestedCalls are the calls this tool made to other tools. Kept for the session record; not sent to the model.
	// upstream: .upstream/v0.99.1/packages/ai/src/types.ts:604
	NestedCalls *ai.NestedToolCalls `json:"nestedCalls,omitempty"`
	IsError     bool                `json:"isError"`
	Timestamp   int64               `json:"timestamp"`
}

func (m ToolResultMessage) Text() string {
	var text strings.Builder
	for _, block := range m.Content {
		if value, ok := block.(ai.TextContent); ok {
			text.WriteString(value.Text)
		}
	}
	return text.String()
}

// Result is the result a display renders for this message: its content, details and error flag. A message with
// details: null gives a result that holds details: null.
func (m ToolResultMessage) Result() AgentToolResult {
	result := AgentToolResult{Content: m.Content, Details: m.Details, IsError: m.IsError}
	if m.Details == nil && m.DetailsNull {
		result.MemberOrder = []string{"content", "details"}
	}
	return result
}

func (m ToolResultMessage) Images() []ai.ImageContent {
	var images []ai.ImageContent
	for _, block := range m.Content {
		if value, ok := block.(ai.ImageContent); ok {
			images = append(images, value)
		}
	}
	return images
}

// AgentMessage is the union of all message types. Only one field is non-nil.
type AgentMessage struct {
	System     *ai.SystemMessage
	User       *UserMessage
	Assistant  *AssistantMessage
	ToolResult *ToolResultMessage
	// Custom message types (bash execution, branch summary, etc.)
	Custom map[string]any // raw JSON for custom types
}

// Role returns the role string.
func (m AgentMessage) Role() string {
	switch {
	case m.System != nil:
		return "system"
	case m.User != nil:
		return m.User.Role
	case m.Assistant != nil:
		return m.Assistant.Role
	case m.ToolResult != nil:
		return m.ToolResult.Role
	case m.Custom != nil:
		if r, ok := m.Custom["role"].(string); ok {
			return r
		}
	}
	return ""
}

func (m AgentMessage) ContentBlocks() []ai.ContentBlock {
	var blocks []ai.ContentBlock
	switch {
	case m.System != nil:
		switch content := m.System.Content.(type) {
		case ai.SystemText:
			blocks = []ai.ContentBlock{ai.TextContent{Text: string(content)}}
		case ai.SystemTextBlocks:
			blocks = make([]ai.ContentBlock, len(content))
			for i, block := range content {
				blocks[i] = block
			}
		}
	case m.User != nil:
		switch content := m.User.Content.(type) {
		case ai.UserText:
			blocks = []ai.ContentBlock{ai.TextContent{Text: string(content)}}
		case ai.UserContentBlocks:
			blocks = make([]ai.ContentBlock, len(content))
			for i, block := range content {
				blocks[i] = block
			}
		}
	case m.Assistant != nil:
		message := m.Assistant.Observe()
		blocks = make([]ai.ContentBlock, len(message.Content))
		for i, block := range message.Content {
			blocks[i] = block
		}
	case m.ToolResult != nil:
		blocks = make([]ai.ContentBlock, len(m.ToolResult.Content))
		for i, block := range m.ToolResult.Content {
			blocks[i] = block
		}
	}
	return blocks
}

// ─── Tool Interface ───────────────────────────────────────────────────────────

// ToolUpdateCallback is called when a tool emits a streaming progress update. The partial result is the complete-so-far AgentToolResult, which the tool_execution_update event carries verbatim as `partialResult`.
// upstream: packages/agent/src/types.ts AgentToolUpdateCallback, agent-loop.ts:778-786
type ToolUpdateCallback func(partial AgentToolResult)

// ToolUpdateSink receives the progress updates of a tool call that [RunToolCall] runs. A non-nil error is upstream's rejected promise: the update is not delivered further, later updates still reach the sink, and the call fails with the first error once the tool returns.
//
// upstream: agent-loop.ts:685 (ToolUpdateSink), 820-849 (executePreparedToolCall)
type ToolUpdateSink = func(partial AgentToolResult) error

// ToolExecutionMode controls parallelism.
type ToolExecutionMode string

const (
	ToolModeSequential ToolExecutionMode = "sequential"
	ToolModeParallel   ToolExecutionMode = "parallel"
)

// AgentToolResult carries the ordered text/image blocks returned by a tool.
// Ports packages/agent/src/types.ts.
type AgentToolResult struct {
	// MemberOrder names the members of the result object in the order its tool wrote them (content, details,
	// isError, structuredContent, usage, terminate). Upstream hands the tool's own object on, so JSON.stringify
	// writes its members in that order; a result without it (a built-in tool, a Go tool) has the declared order.
	// upstream: agent-loop.ts:778-786, 912-919
	MemberOrder []string
	Content     []ai.ToolResultMessageContent
	Details     any
	// StructuredContent is the machine-readable result of a tool that declares
	// an output schema. upstream: structuredContent?: JsonValue
	StructuredContent json.RawMessage
	// IsError is upstream's `result.isError`: the tool returned a failure instead of throwing it, so
	// details and structuredContent are kept. A thrown error, an unknown tool, invalid arguments and a
	// blocked call produce a result without it; the failure is reported by ToolExecutionEndEvent.IsError
	// and AgentToolCallOutcome.IsError. upstream: types.ts:436-441, agent-loop.ts:906-910
	IsError bool
	// Thrown marks a result that stands for an error upstream's tool throws. The agent reports the call as failed, drops
	// the result's IsError and Thrown, and keeps the rest, which is createErrorToolResult's content and empty details
	// (agent-loop.ts:906-910). A tool that returns a Go error has the same effect.
	Thrown bool
	// StructuredContentAppended records that afterToolCall gave a result that had no structuredContent one. Upstream's
	// `{...result}` spread keeps the tool's key order and assigning an existing key keeps its position, so only a key the
	// hook adds follows every key the tool set (agent-loop.ts:877-889). It only orders the serialized result.
	StructuredContentAppended bool
	// Usage is the usage of the tool execution itself, if available. It is
	// not used for main LLM context accounting. upstream: usage?: Usage
	Usage *ai.Usage
	// Terminate hints that the agent should stop after the current tool
	// batch. Early termination happens only when every finalized result in
	// the batch sets it. upstream: terminate?: boolean
	Terminate bool
	// Preview is an optional short summary shown when the tool result is
	// collapsed in the TUI. When non-empty, overrides the default
	// tail-preview behavior. Extensions can set this to provide a
	// custom collapsed view (e.g. a table header, a line count, or a
	// brief summary) without implementing a full BodyRenderer.
	// pig extension: no upstream equivalent (Pi extensions use in-process
	// renderResult callbacks; subprocess extensions need a serializable
	// alternative).
	Preview string
}

// MemberNames lists the members of the result object this result holds (content, details, structuredContent, isError,
// usage, terminate), in the order upstream's object keeps them. Without a recorded MemberOrder that is the order the
// built-in tools build theirs in, with a structuredContent a hook added last. With one it is the tool's own order, and a
// member the tool did not set, which a hook added, follows the tool's in the order of the hook's spread, `{...result,
// content, details, usage, terminate}`, then the structuredContent assigned after it (agent-loop.ts:877-889). Content is
// always listed. A member MemberOrder names is held even when its value is a zero value (details or usage null, isError or
// terminate false): the tool wrote it, so upstream's object has it and JSON.stringify writes it.
// upstream: agent-loop.ts:778-786, 877-889, 912-919; coding-agent/src/core/tools/bash.ts:403-409
func (r AgentToolResult) MemberNames() []string {
	written := func(name string) bool { return slices.Contains(r.MemberOrder, name) }
	held := map[string]bool{
		"content":           true,
		"details":           r.Details != nil || written("details"),
		"structuredContent": len(r.StructuredContent) > 0,
		"isError":           r.IsError || written("isError"),
		"usage":             r.Usage != nil || written("usage"),
		"terminate":         r.Terminate || written("terminate"),
	}
	var names []string
	add := func(name string) {
		if held[name] && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(r.MemberOrder) == 0 {
		for _, name := range []string{"content", "details"} {
			add(name)
		}
		if !r.StructuredContentAppended {
			add("structuredContent")
		}
		for _, name := range []string{"isError", "usage", "terminate", "structuredContent"} {
			add(name)
		}
		return names
	}
	for _, name := range r.MemberOrder {
		add(name)
	}
	for _, name := range []string{"content", "details", "usage", "terminate", "structuredContent", "isError"} {
		add(name)
	}
	return names
}

// DetailsNull reports a result that holds details: null: the tool wrote the details member with no value. A Go tool
// that leaves Details nil and records no MemberOrder holds no details.
func (r AgentToolResult) DetailsNull() bool {
	return r.Details == nil && slices.Contains(r.MemberNames(), "details")
}

// Text joins text blocks for text-only displays; Content retains their boundaries and image positions.
func (r AgentToolResult) Text() string {
	var text []string
	for _, block := range r.Content {
		if value, ok := block.(ai.TextContent); ok {
			text = append(text, value.Text)
		}
	}
	return strings.Join(text, "\n")
}

// Images selects image blocks for displays with a separate image area.
func (r AgentToolResult) Images() []ai.ImageContent {
	return (ToolResultMessage{Content: r.Content}).Images()
}

// AgentTool is the interface that all tools must implement.
type AgentTool interface {
	// Name is the tool's name as known to the LLM.
	Name() string
	// Label returns a human-readable display name for the tool header.
	// If empty, Name() is used for display. Mirrors upstream AgentTool.label.
	Label() string
	// Schema returns the JSON Schema for the tool's parameters.
	Schema() ai.ToolSchema
	// Execute runs the tool.
	Execute(ctx context.Context, toolCallID string, params json.RawMessage,
		onUpdate ToolUpdateCallback) (AgentToolResult, error)
	// ExecutionMode returns the tool's parallelism preference.
	ExecutionMode() ToolExecutionMode
}

// ArgumentPreparer is an optional interface tools may implement to
// normalize / coerce their raw input BEFORE schema validation runs.
// Mirrors upstream's per-tool `prepareArguments` hook
// (.upstream/current/packages/coding-agent/src/core/tools/edit.ts:308,
// `parameters: editSchema, ... prepareArguments: prepareEditArguments`).
//
// Use cases:
//   - edit tool: rewrites the legacy `{path, oldText, newText}` flat
//     input shape into `{path, edits:[{oldText, newText}]}` so the
//     strict editSchema accepts it (LLMs like Opus 4.6 / GLM-5.1 still
//     emit the flat shape).
//   - any future tool that accepts multiple wire shapes for the same
//     semantic input.
//
// Without this hook, schema validation in dispatch() rejects valid
// legacy inputs before tool.Execute() ever runs, breaking parity with
// upstream which normalizes first and validates after.
type ArgumentPreparer interface {
	PrepareArguments(raw json.RawMessage) (json.RawMessage, error)
}

// ─── Agent Events ─────────────────────────────────────────────────────────────

// AgentEvent is emitted by the agent loop to notify observers.
type AgentEvent interface{ agentEvent() }

type AgentStartEvent struct{}
type AgentEndEvent struct {
	Messages  []AgentMessage
	WillRetry bool
}

// AgentSettledEvent fires once an agent run has fully settled: no automatic
// retry, compaction, or queued continuation will follow. Upstream emits it from
// the _runAgentPrompt finally (agent-session.ts:1073) to both the extension
// runner and the session event stream.
type AgentSettledEvent struct{}

// QueueUpdateEvent reports the current pending steering and follow-up text.
// It mirrors the coding-agent session queue_update event.
type QueueUpdateEvent struct {
	Steering []string
	FollowUp []string
}

// SessionInfoChangedEvent reports the effective Session name; an empty name clears it.
type SessionInfoChangedEvent struct {
	Name string
}

// BashExecutionUpdateEvent reports one command-output delta. A nil ID omits the correlation identifier; a non-nil empty ID remains explicit.
type BashExecutionUpdateEvent struct {
	ID    *string
	Delta string
}

// ThinkingLevelChangedEvent reports an effective reasoning-level change.
type ThinkingLevelChangedEvent struct {
	Level ai.ThinkingLevel
}

type TurnStartEvent struct {
	TurnIndex int
	Timestamp time.Time
}
type TurnEndEvent struct {
	TurnIndex          int
	Message            AgentMessage
	ToolResults        []ToolResultMessage
	MessageEntryID     string
	ToolResultEntryIDs []string
}
type MessageStartEvent struct {
	Message     AgentMessage
	observation *ai.StreamObservation
}
type MessageUpdateEvent struct {
	Message               AgentMessage
	AssistantMessageEvent ai.AssistantMessageEvent
	observation           *ai.StreamObservation
}
type MessageEndEvent struct {
	Message     AgentMessage
	observation *ai.StreamObservation
}

// RefreshEvent returns the message event with the assistant message's and the provider event's partial fields set to the stream state at this call. A host dispatcher calls it when it delivers a retained event to a listener after an awaited step. Other events are returned unchanged.
func RefreshEvent(event AgentEvent) AgentEvent {
	switch value := event.(type) {
	case MessageStartEvent:
		value.Message.Assistant = value.Message.Assistant.refreshed()
		return value
	case MessageUpdateEvent:
		value.Message.Assistant = value.Message.Assistant.refreshed()
		if value.AssistantMessageEvent != nil {
			value.AssistantMessageEvent = ai.RefreshEvent(value.AssistantMessageEvent)
		}
		return value
	case MessageEndEvent:
		value.Message.Assistant = value.Message.Assistant.refreshed()
		return value
	default:
		return event
	}
}

// EventObservation returns the attached stream scope for synchronous host dispatch. Session transfers that scope before notifying its synchronous subscribers; it is not message data or wire metadata.
func EventObservation(event AgentEvent) *ai.StreamObservation {
	switch event := event.(type) {
	case MessageStartEvent:
		return event.observation
	case MessageUpdateEvent:
		return event.observation
	case MessageEndEvent:
		return event.observation
	default:
		return nil
	}
}

// WithEventObservation carries a stream scope across synchronous host dispatch without changing message identity or serialized fields. Events outside the message lifecycle are unchanged.
func WithEventObservation(event AgentEvent, observation *ai.StreamObservation) AgentEvent {
	switch value := event.(type) {
	case MessageStartEvent:
		value.observation = observation
		return value
	case MessageUpdateEvent:
		value.observation = observation
		return value
	case MessageEndEvent:
		value.observation = observation
		return value
	default:
		return event
	}
}

type ToolExecutionStartEvent struct {
	ToolCallID string
	ToolName   string
	ToolLabel  string // human-readable display name (empty = use ToolName)
	Args       json.RawMessage
	// ParentToolCallID is set on the events of a call another tool made through `ctx.executeTool()`. upstream: agent-session.ts:182-189 (WithParentToolCallId)
	ParentToolCallID string
}
type ToolExecutionUpdateEvent struct {
	ToolCallID string
	ToolName   string
	// PartialResult is the tool's streaming progress. Subsequent updates carry the full accumulated result so far (upstream's onUpdate contract: every update is a complete-so-far snapshot, not a delta). Tool components in the TUI replace their visible body with each update, giving a live tail. upstream: agent-loop.ts:778-786
	PartialResult AgentToolResult
	// Args is the original tool call arguments (same as ToolExecutionStartEvent.Args).
	// Passed through for extensions that need to correlate updates with inputs.
	Args json.RawMessage
	// ParentToolCallID is set on the events of a call another tool made through `ctx.executeTool()`. upstream: agent-session.ts:182-189 (WithParentToolCallId)
	ParentToolCallID string
}
type ToolExecutionEndEvent struct {
	ToolCallID string
	ToolName   string
	Result     AgentToolResult
	// IsError is upstream's `event.isError`: the call failed, whether the tool threw, returned `isError`, or an
	// afterToolCall hook said so. It is not Result.IsError. upstream: agent-loop.ts:912-919
	IsError  bool
	Duration time.Duration
	// ParentToolCallID is set on the events of a call another tool made through `ctx.executeTool()`. upstream: agent-session.ts:182-189 (WithParentToolCallId)
	ParentToolCallID string
}

// TimingEvent retains the former diagnostic event shape for source compatibility.
// The agent no longer emits timing events, matching Pi's event stream.
//
// Deprecated: Read Agent.Timings or observe TurnEndEvent and ToolExecutionEndEvent.
type TimingEvent = timingEvent

type timingEvent struct {
	Kind     string
	Name     string
	Duration time.Duration
	Snapshot TimingSnapshot
}

func (timingEvent) agentEvent() {}

// CompactionStartEvent fires when auto-compaction or /compact begins.
// Mirrors upstream compaction_start event (agent-session.ts).
type CompactionStartEvent struct {
	Reason string // "manual" | "threshold" | "overflow"
}

// CompactionEndEvent fires when compaction completes, aborts, or errors.
// Mirrors upstream compaction_end event (agent-session.ts).
type CompactionEndEvent struct {
	Reason               string
	Aborted              bool
	WillRetry            bool
	Summary              string
	FirstKeptEntryID     string
	TokensBefore         int
	EstimatedTokensAfter int
	Usage                *ai.Usage
	Details              any
	ErrorMessage         string
}

func (AgentStartEvent) agentEvent()           {}
func (AgentEndEvent) agentEvent()             {}
func (AgentSettledEvent) agentEvent()         {}
func (BashExecutionUpdateEvent) agentEvent()  {}
func (QueueUpdateEvent) agentEvent()          {}
func (SessionInfoChangedEvent) agentEvent()   {}
func (ThinkingLevelChangedEvent) agentEvent() {}
func (TurnStartEvent) agentEvent()            {}
func (TurnEndEvent) agentEvent()              {}
func (MessageStartEvent) agentEvent()         {}
func (MessageUpdateEvent) agentEvent()        {}
func (MessageEndEvent) agentEvent()           {}
func (ToolExecutionStartEvent) agentEvent()   {}
func (ToolExecutionUpdateEvent) agentEvent()  {}
func (ToolExecutionEndEvent) agentEvent()     {}

// AutoRetryStartEvent fires when pig is about to retry a failed request
// due to a transient error (overload, rate limit, server error).
// Mirrors upstream auto_retry_start (agent-session.ts:130).
type AutoRetryStartEvent struct {
	Attempt      int
	MaxAttempts  int
	DelayMs      int
	ErrorMessage string
}

// AutoRetryEndEvent fires when an auto-retry cycle completes (success or
// exhausted). Mirrors upstream auto_retry_end (agent-session.ts:131).
type AutoRetryEndEvent struct {
	Success    bool
	Attempt    int
	FinalError string // set when Success==false
}

// SummarizationRetryScheduledEvent fires before the backoff sleep of each retry
// of a compaction or branch-summary summarization call. Mirrors upstream
// summarization_retry_scheduled (agent-session.ts:167).
type SummarizationRetryScheduledEvent struct {
	Attempt      int
	MaxAttempts  int
	DelayMs      int
	ErrorMessage string
}

// SummarizationRetryAttemptStartEvent fires after the backoff sleep, right
// before the retried summarization call. Source is "compaction" or
// "branchSummary"; Reason ("manual"|"threshold"|"overflow") applies to
// compaction and lets the TUI recreate the right status indicator. Mirrors
// upstream summarization_retry_attempt_start (agent-session.ts:173).
type SummarizationRetryAttemptStartEvent struct {
	Source string
	Reason string
}

// SummarizationRetryFinishedEvent fires once when a retried summarization loop
// ends. Mirrors upstream summarization_retry_finished (agent-session.ts:179).
type SummarizationRetryFinishedEvent struct{}

// EntryAppendedEvent reports a Session entry appended outside the agent loop,
// such as cache-warming usage. Entry is the persisted entry's JSON. Mirrors
// upstream AgentSessionEvent entry_appended (agent-session.ts:178).
type EntryAppendedEvent struct {
	Entry json.RawMessage
}

func (EntryAppendedEvent) agentEvent()                  {}
func (CompactionStartEvent) agentEvent()                {}
func (CompactionEndEvent) agentEvent()                  {}
func (AutoRetryStartEvent) agentEvent()                 {}
func (AutoRetryEndEvent) agentEvent()                   {}
func (SummarizationRetryScheduledEvent) agentEvent()    {}
func (SummarizationRetryAttemptStartEvent) agentEvent() {}
func (SummarizationRetryFinishedEvent) agentEvent()     {}

// ─── Before/After Hooks ───────────────────────────────────────────────────────

// ToolCallHookResult controls whether tool execution proceeds.
//
// Mirrors upstream BeforeToolCallResult. Terminate on a blocked call joins the
// batch early-termination rule: the agent stops after the batch only when
// every finalized result in it terminates.
type ToolCallHookResult struct {
	Block     bool
	Reason    string
	Terminate bool
	// Mutated args (if the hook wants to rewrite them). They execute without
	// revalidation, as upstream executes a hook's mutated args.
	Args json.RawMessage
}

// BeforeToolCallHook is called before a tool executes.
// Return ToolCallHookResult{Block: true} to prevent execution.
type BeforeToolCallHook func(ctx context.Context, toolCallID, toolName string, args json.RawMessage) ToolCallHookResult

// AfterToolCallResult carries per-tool overrides returned by AfterToolCallHook.
// Mirrors upstream types.ts AfterToolCallResult (agent-loop.ts:617-640).
// Merge semantics: field-by-field replace; no deep merge. Nil fields keep the original; a non-nil empty Content replaces it with an empty array.
//
//   - Content: if non-nil, replaces the ordered content, including an empty array.
//   - IsError: if non-nil, replaces the tool result error flag.
//   - Usage: if non-nil, replaces the tool result usage.
//   - Terminate: if non-nil, replaces the early-termination hint. The agent
//     stops after the batch only when every finalized result terminates
//     (upstream shouldTerminateToolBatch).
type AfterToolCallResult struct {
	Content   []ai.ToolResultMessageContent
	Details   any
	IsError   *bool // nil = keep original
	Usage     *ai.Usage
	Terminate *bool
	// StructuredContent replaces the result's structured content. When Content is replaced without it, the structured content is dropped, because it may no longer match the content.
	StructuredContent json.RawMessage
}

// AfterToolCallHook is called after a tool executes and may override its result.
// Full upstream contract, including terminate signals and result overrides.
// Replaces the old void-return signature which had no override capability.
// upstream: agent-loop.ts afterToolCall({ toolCall, args, result, isError })
type AfterToolCallHook func(ctx context.Context, toolCallID, toolName string, args json.RawMessage, result AgentToolResult) AfterToolCallResult

// ─── Agent ────────────────────────────────────────────────────────────────────

// AgentOptions configures the agent loop.
type AgentOptions struct {
	Model           *ai.Model
	Tools           []AgentTool
	SystemPrompt    string
	MaxTurns        int // 0 = unlimited, as upstream, which has no turn cap
	ThinkingLevel   ai.ThinkingLevel
	ThinkingBudgets *ai.ThinkingBudgets
	Transport       ai.Transport
	// SessionID is forwarded to the provider for prompt caching
	// (OpenAI prompt_cache_key). Set by the session wrapper when
	// the session has a stable identifier.
	SessionID string
	// SessionFile, when set, reports the session file tools may expose as
	// PI_SESSION_FILE. Nil or an empty result exports nothing.
	SessionFile func() string

	// SteeringMode controls how steering messages are drained (default: one-at-a-time).
	// Mirrors upstream packages/agent/src/agent.ts:104.
	SteeringMode QueueMode
	// FollowUpMode controls how follow-up messages are drained (default: one-at-a-time).
	// Mirrors upstream packages/agent/src/agent.ts:105.
	FollowUpMode QueueMode

	BeforeToolCall []BeforeToolCallHook
	AfterToolCall  []AfterToolCallHook
	// PrepareToolResult processes the final extension-modified result before
	// events and persistence. Processing failures preserve the original result.
	PrepareToolResult func(context.Context, AgentToolResult) AgentToolResult
	// FinishTurn runs after a turn's assistant message and tool results and
	// before TurnEndEvent; it may end the run or request one more request.
	FinishTurn FinishTurn
	// PrepareRequest runs immediately before every provider request and may
	// replace the context, model, and thinking level for the run.
	PrepareRequest PrepareRequest
	// ConvertToLlm converts the transformed agent context to provider messages.
	// The run waits for it and reports conversion failures through its lifecycle.
	// Nil uses the standard message conversion.
	ConvertToLlm func([]AgentMessage) ([]ai.Message, error)
	// TransformLLMMessages post-processes the provider messages that the
	// built-in conversion produced for each request. The coding session uses
	// it as upstream createAgentSession wraps convertToLlm
	// (convertToLlmWithBlockImages in sdk.ts).
	TransformLLMMessages func([]ai.Message) []ai.Message
	// PrepareNextTurn runs before the next turn starts when the loop
	// continues, and may replace or extend the next request's state.
	PrepareNextTurn PrepareNextTurn
	// PreparePrompt lets the owning Session prepare new prompt messages before
	// their lifecycle events and persistence. It does not run for continuation
	// or queued steering/follow-up messages.
	PreparePrompt func(context.Context, []AgentMessage) ([]AgentMessage, error)
	// ToolExecution is the batch strategy for assistant messages with several
	// tool calls (default parallel). A tool whose ExecutionMode is sequential
	// forces its batch to run sequentially. Mirrors upstream Agent.toolExecution.
	ToolExecution ToolExecutionMode

	// EventCh receives agent events. If nil, events are discarded. Every
	// event is delivered, including after the run's context is cancelled, as
	// upstream awaits every listener; emit blocks until the event is received
	// or EventDone is closed.
	EventCh chan<- AgentEvent
	// EventDone, when closed, reports that EventCh has no receiver anymore.
	// Events emitted after that are dropped instead of blocking the agent.
	EventDone <-chan struct{}

	// OnEvent, if set, runs synchronously for every event before the event is
	// persisted or delivered, as upstream Agent awaits its listeners before it
	// continues. It may replace a message_end message in place (mutating the
	// pointed-to message or custom map), and the replacement is what agent
	// state, persistence and EventCh observe.
	OnEvent func(AgentEvent)
	// AfterEvent runs after persistence and channel publication, before the loop polls queued work. An error fails the same run.
	AfterEvent func(AgentEvent) error
	// StreamFn sends each provider request. Nil uses DefaultStreamFn or the model's native provider.
	// Mirrors upstream AgentOptions.streamFn.
	StreamFn StreamFn
	// DefaultStreamFn supplies the host's stock request path without making it a caller override. Nil uses the configured host default.
	DefaultStreamFn StreamFn

	// OnMessagePersist, if set, is invoked exactly once for each NEW message
	// the agent produces during a turn: the user prompt, steering / follow-up
	// user messages, assistant messages (including aborted/error ones), and
	// tool-result messages: in production order. It is driven by message_end in
	// emit() and is NOT called for replayed/resumed context (the runLoop replay
	// emits message_end only for this run's new messages, never loaded history).
	// Mirrors upstream's single incremental persistence site (agent-session.ts
	// message_end handler) so a turn interrupted mid-flight is durably recorded
	// instead of lost on the next resume. An error fails the run the way a
	// throwing message_end listener does upstream: the loop stops and the run
	// ends with an error assistant message.
	OnMessagePersist func(AgentMessage) error
	// OnProviderStreamEvent is forwarded to every provider request. Mirrors upstream AgentOptions.onProviderStreamEvent (packages/agent/src/agent.ts:122).
	OnProviderStreamEvent func(ctx context.Context, data any, model *ai.Model) error
}

// StreamFn starts one provider request for an already normalized transcript.
// Mirrors upstream StreamFn.
type StreamFn func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error)

// Agent is the agent loop.
//
// Mirrors upstream packages/agent/src/agent.ts (class Agent).
type Agent struct {
	opts               AgentOptions
	stateMu            sync.RWMutex
	stateRevision      uint64
	forcedSystemPrompt *string
	// messagesMu guards writes to messages and MessagesSnapshot reads, so
	// other goroutines can observe the transcript while a run appends to it.
	messagesMu sync.RWMutex
	// toolsMu guards opts.Tools: an extension registers a tool, and the Session replaces the tool list, while a run reads it.
	toolsMu       sync.RWMutex
	messages      []AgentMessage
	timings       *Recorder
	steeringQueue *PendingMessageQueue
	followUpQueue *PendingMessageQueue

	// pendingNextTurn holds messages an extension deferred with
	// deliverAs "nextTurn". Upstream keeps these apart from the steering and
	// follow-up queues because they are not continuations of a running turn:
	// they are context for the next user prompt, injected beside the user
	// message before the first model call (agent-session.ts:1226).
	pendingNextTurn   []AgentMessage
	pendingNextTurnMu sync.Mutex
	streaming         bool // true while Send/Continue is actively streaming
	runContext        context.Context
	runCancel         context.CancelFunc
	runSignalWatchers []*runSignalWatcher
	stopParentCancel  func() bool
	runDone           chan struct{}
	listeners         []*agentListener
	streamingMessage  *AgentMessage
	pendingToolCalls  []string
	errorMessage      string

	// beforeProviderHook transforms the provider's final wire payload.
	// Mirrors upstream before_provider_request via StreamOptions.OnPayload.
	beforeProviderHook func(payload any, model *ai.Model) (any, error)

	// transformHeaders runs on each provider request's merged HTTP headers.
	// Mirrors upstream buildRequestOptions.transformHeaders (sdk.ts).
	transformHeaders func(context.Context, ai.ProviderHeaders) (ai.ProviderHeaders, error)

	// transformContext fires before each LLM call to let extensions
	// modify the message context. Mirrors upstream context event via
	// runner.emitContext → transformContext in sdk.ts.
	transformContext func(context.Context, []AgentMessage) ([]AgentMessage, error)
}

// NewAgent creates a new Agent.
func NewAgent(opts AgentOptions) *Agent {
	opts.Tools = slices.Clone(opts.Tools)
	if opts.DefaultStreamFn == nil {
		// A native model provider remains the host default when no registry is configured.
		opts.DefaultStreamFn, _ = GetDefaultStreamFn()
	}
	if opts.Model == nil {
		// DEFAULT_MODEL has an explicit empty input list; adapters must not infer text support for it.
		opts.Model = &ai.Model{ID: "unknown", DisplayName: "unknown", Input: []string{}, ProviderMeta: ai.ProviderMetadata{ProviderID: "unknown", API: "unknown"}}
	}
	if opts.ThinkingLevel == "" {
		opts.ThinkingLevel = ai.ThinkingOff
	}
	sm := opts.SteeringMode
	if sm == "" {
		sm = QueueModeOneAtATime
	}
	fm := opts.FollowUpMode
	if fm == "" {
		fm = QueueModeOneAtATime
	}
	if opts.ToolExecution == "" {
		opts.ToolExecution = ToolModeParallel
	}
	a := &Agent{
		opts:          opts,
		timings:       NewRecorder(),
		steeringQueue: NewPendingMessageQueue(sm),
		followUpQueue: NewPendingMessageQueue(fm),
	}
	if initial := ai.CreateInitialSystemMessage(opts.SystemPrompt, a.toolDeclarations()); initial != nil {
		a.messages = []AgentMessage{{System: initial}}
	}
	return a
}

// Timings returns the agent's timing recorder. Read-only consumers
// (status line, /cost, --diagnose) hold this reference for the
// session's lifetime and call Snapshot()/Elapsed() on demand.
func (a *Agent) Timings() *Recorder { return a.timings }

// Tools returns the agent's current tool list. Defensive copy.
func (a *Agent) Tools() []AgentTool {
	return a.toolList()
}

// SetTools replaces the active tool list for subsequent turns.
func (a *Agent) SetTools(tools []AgentTool) {
	a.toolsMu.Lock()
	defer a.toolsMu.Unlock()
	a.opts.Tools = append([]AgentTool(nil), tools...)
}

// toolList is a copy of the current tool list.
func (a *Agent) toolList() []AgentTool {
	a.toolsMu.RLock()
	defer a.toolsMu.RUnlock()
	return slices.Clone(a.opts.Tools)
}

// SetSystemPrompt projects a replacement system prompt onto subsequent provider requests without rewriting transcript history.
func (a *Agent) SetSystemPrompt(prompt string) {
	a.messagesMu.Lock()
	defer a.messagesMu.Unlock()
	a.opts.SystemPrompt = prompt
	a.forcedSystemPrompt = new(prompt)
}

// ClearSystemPrompt removes a SetSystemPrompt projection, so later requests
// use the transcript's own system prompt again.
func (a *Agent) ClearSystemPrompt() {
	a.messagesMu.Lock()
	defer a.messagesMu.Unlock()
	a.forcedSystemPrompt = nil
}

// SystemPromptOverride returns only the explicit provider projection and its presence, not replayed transcript instructions.
func (a *Agent) SystemPromptOverride() (string, bool) {
	a.messagesMu.RLock()
	defer a.messagesMu.RUnlock()
	if a.forcedSystemPrompt == nil {
		return "", false
	}
	return *a.forcedSystemPrompt, true
}

func (a *Agent) systemPromptOverride() *string {
	a.messagesMu.RLock()
	defer a.messagesMu.RUnlock()
	return a.forcedSystemPrompt
}

// ErrNoModelSelected is returned when a turn is requested with no usable model.
// Upstream agent-session.prompt() throws formatNoModelSelectedMessage() before
// streaming; pig surfaces the same condition as an error rather than panicking
// on a nil model in runLoop.
var ErrNoModelSelected = errors.New("no model selected")

// ErrAlreadyProcessingPrompt is returned when a prompt is sent while a run is
// active. Mirrors upstream Agent.prompt's "Agent is already processing a
// prompt" error: queue the message with Steer or FollowUp instead.
var ErrAlreadyProcessingPrompt = errors.New("Agent is already processing a prompt. Use steer() or followUp() to queue messages, or wait for completion.")

// ErrAlreadyProcessing is returned when Continue or Reset is called while a
// run is active. Mirrors upstream Agent.continue and Agent.reset.
var ErrAlreadyProcessing = errors.New("Agent is already processing.")

// ErrNoMessagesToContinue is returned by Continue on an empty transcript.
// Mirrors upstream Agent.continue's "No messages to continue from".
var ErrNoMessagesToContinue = errors.New("No messages to continue from")

// ErrToolResultsUnanswered reports that a run ended with tool results the
// model never answered although no tool asked to terminate the run. It guards
// the invariant that a run never ends silently mid-task.
var ErrToolResultsUnanswered = errors.New("agent run ended after tool results without a model response")

// ErrMaxTurnsReached reports that a run stopped at AgentOptions.MaxTurns with
// tool results still waiting for a model response.
var ErrMaxTurnsReached = errors.New("agent stopped at its turn limit with tool results still unanswered")

// ensureModel requires a model and either an injected stream function or a provider runtime.
func (a *Agent) ensureModel() error {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	if a.opts.Model == nil || (a.opts.StreamFn == nil && a.opts.DefaultStreamFn == nil && a.opts.Model.Provider == nil) {
		return ErrNoModelSelected
	}
	return nil
}

// beginRun claims the agent for one run, or returns busy when a run is active.
// Mirrors upstream Agent's activeRun guard.
func (a *Agent) beginRun(ctx context.Context, busy error) error {
	if err := a.claimRun(ctx, busy); err != nil {
		return err
	}
	a.notifyRunSignalObservers()
	return nil
}

func (a *Agent) claimRun(ctx context.Context, busy error) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.streaming {
		return busy
	}
	// Detach the parent's cancellation registration at settlement without
	// aborting a successfully completed run's retained signal.
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	deadline, hasDeadline := ctx.Deadline()
	signal := &runDeadlineContext{Context: ai.WithStreamContinuations(runContext), parent: ctx, deadline: deadline, hasDeadline: hasDeadline}
	signal.active.Store(true)
	a.runContext = signal
	a.runCancel = cancel
	a.stopParentCancel = context.AfterFunc(ctx, cancel)
	if ctx.Err() != nil {
		cancel()
	}
	a.runDone = make(chan struct{})
	a.streaming = true
	a.streamingMessage = nil
	return nil
}

// finishRun releases the claim beginRun took.
func (a *Agent) finishRun() {
	a.releaseRun()
	a.notifyRunSignalObservers()
}

func (a *Agent) releaseRun() {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.stopParentCancel()
	signal := a.runContext.(*runDeadlineContext)
	if signal.parent.Err() != nil {
		a.runCancel()
	}
	signal.active.Store(false)
	a.streaming = false
	a.streamingMessage = nil
	a.pendingToolCalls = nil
	a.runContext = nil
	a.runCancel = nil
	a.stopParentCancel = nil
	close(a.runDone)
	a.runDone = nil
	a.listeners = slices.DeleteFunc(a.listeners, func(listener *agentListener) bool { return listener.removed.Load() })
}

// Send sends a plain-text user message and runs the agent loop until the LLM
// stops calling tools. Steering and follow-up messages queued via
// Steer/FollowUp are polled at the appropriate points in the loop.
func (a *Agent) Send(ctx context.Context, content string) ([]AgentMessage, error) {
	return a.SendContent(ctx, []ai.UserContentBlock{ai.TextContent{Text: content}})
}

// SendContent is the structured-content variant of Send.
func (a *Agent) SendContent(ctx context.Context, content []ai.UserContentBlock) ([]AgentMessage, error) {
	run, err := a.BeginSendContent(ctx, content)
	if err != nil {
		return nil, err
	}
	return run.Run()
}

// PromptRun owns a claimed prompt and its cancellation context. Start dispatches agent_start; Run drains the remaining events and releases the claim. The owner must call Run exactly once, even if Start fails or the context is cancelled.
type PromptRun struct {
	agent            *Agent
	ctx              context.Context
	content          []ai.UserContentBlock
	messages         []AgentMessage
	suppliedMessages bool
	prompts          []AgentMessage
	started          bool
	prepared         bool
	startErr         error
}

// Start prepares the prompt and dispatches agent_start without requesting a Provider response or committing unfinished prompt messages. Repeated calls return the first result.
func (r *PromptRun) Start() error {
	if r.started {
		return r.startErr
	}
	r.started = true
	a := r.agent
	var messages []AgentMessage
	if r.suppliedMessages {
		messages = r.messages
		if a.opts.PreparePrompt != nil {
			messages, r.startErr = a.opts.PreparePrompt(r.ctx, messages)
		}
	} else {
		messages, r.startErr = a.prepareContent(r.ctx, r.content)
	}
	if r.startErr != nil {
		return r.startErr
	}
	r.prompts = a.declareToolChanges(a.MessagesSnapshot(), messages)
	r.prepared = true
	a.stateMu.Lock()
	a.errorMessage = ""
	a.stateMu.Unlock()
	err, failure := catchRunFailure(func() error { a.emit(AgentStartEvent{}); return nil })
	if failure != nil {
		err = a.handleRunFailure(failure, r.ctx.Err() != nil, a.Model())
	}
	r.startErr = err
	return err
}

// Run drains the claimed prompt and releases its active-run state, including when Start failed. The owner calls Run once; it calls Start when needed.
func (r *PromptRun) Run() ([]AgentMessage, error) {
	defer r.agent.finishRun()
	if err := r.Start(); err != nil {
		if r.prepared {
			return r.agent.messages, err
		}
		return nil, err
	}
	cfg := r.agent.createLoopConfig(false)
	cfg.agentStarted = true
	return r.agent.runLoop(r.ctx, cfg, r.prompts)
}

// BeginSendContent claims the agent and binds its cancellation context synchronously without starting events. Pending preflight is not an active run; a claimed prompt is active before its first awaited listener finishes.
func (a *Agent) BeginSendContent(ctx context.Context, content []ai.UserContentBlock) (*PromptRun, error) {
	if err := a.beginRun(ctx, ErrAlreadyProcessingPrompt); err != nil {
		return nil, err
	}
	if err := a.ensureModel(); err != nil {
		a.finishRun()
		return nil, err
	}
	return &PromptRun{agent: a, ctx: a.Signal(), content: slices.Clone(content)}, nil
}

// BeginSendMessages claims a turn for already-built messages without starting events. The owner calls Run exactly once, even if Start fails, just as for BeginSendContent.
func (a *Agent) BeginSendMessages(ctx context.Context, messages []AgentMessage) (*PromptRun, error) {
	if err := a.beginRun(ctx, ErrAlreadyProcessingPrompt); err != nil {
		return nil, err
	}
	if err := a.ensureModel(); err != nil {
		a.finishRun()
		return nil, err
	}
	return &PromptRun{agent: a, ctx: a.Signal(), messages: messages, suppliedMessages: true}, nil
}

func (a *Agent) prepareContent(ctx context.Context, content []ai.UserContentBlock) ([]AgentMessage, error) {
	userMsg := AgentMessage{
		User: &UserMessage{
			Role:      RoleUser,
			Content:   append(ai.UserContentBlocks(nil), content...),
			Timestamp: time.Now().UnixMilli(),
		},
	}
	msgs := append([]AgentMessage{userMsg}, a.takePendingNextTurn()...)
	if a.opts.PreparePrompt != nil {
		var err error
		msgs, err = a.opts.PreparePrompt(ctx, msgs)
		if err != nil {
			return nil, err
		}
	}
	return msgs, nil
}

// SendMessages seeds a turn with already-built messages instead of a user
// prompt, mirroring upstream agent.prompt(messages) (agent-session.ts:1063).
// An extension delivering a custom message while the agent is idle needs a turn
// to start from that message; without this the message can only be enqueued for
// a turn that may never run.
func (a *Agent) SendMessages(ctx context.Context, msgs []AgentMessage) ([]AgentMessage, error) {
	if len(msgs) == 0 {
		return a.messages, nil
	}
	run, err := a.BeginSendMessages(ctx, msgs)
	if err != nil {
		return nil, err
	}
	return run.Run()
}

// Continue runs the agent loop from the current message state without
// prepending a new user message. Used to retry a turn after auto-compaction
// when an overflow error is recovered by compacting and replaying.
//
// If the last message is an assistant message, Continue first checks the
// steering queue, then the follow-up queue. Without queued input it rejects
// the assistant tail. Mirrors upstream Agent.continue (packages/agent/src/agent.ts).
func (a *Agent) Continue(ctx context.Context) ([]AgentMessage, error) {
	if err := a.beginRun(ctx, fmt.Errorf("%w Wait for completion before continuing.", ErrAlreadyProcessing)); err != nil {
		return nil, err
	}
	defer a.finishRun()
	ctx = a.Signal()
	lastMsg := a.lastMessage()
	if lastMsg == nil || slices.IndexFunc(a.messages, func(m AgentMessage) bool { return m.System == nil }) == -1 {
		return a.messages, ErrNoMessagesToContinue
	}
	if lastMsg.Assistant != nil {
		// Upstream: drain steering first, then follow-ups.
		if steered := a.steeringQueue.Drain(); len(steered) > 0 {
			return a.runPromptMessages(ctx, steered, a.createLoopConfig(true))
		}
		if followUps := a.followUpQueue.Drain(); len(followUps) > 0 {
			return a.runPromptMessages(ctx, followUps, a.createLoopConfig(false))
		}
		return a.messages, fmt.Errorf("Cannot continue from message role: assistant")
	}
	return a.runAgentLoopContinue(ctx, a.createLoopConfig(false))
}

// runPromptMessages prepares a prompt batch and runs its lifecycle before the
// first provider request. Mirrors upstream Agent.runPromptMessages.
func (a *Agent) runPromptMessages(ctx context.Context, msgs []AgentMessage, cfg agentLoopConfig) ([]AgentMessage, error) {
	return a.runLoop(ctx, cfg, a.declareToolChanges(a.messages, msgs))
}

func (a *Agent) lastMessage() *AgentMessage {
	if len(a.messages) == 0 {
		return nil
	}
	return &a.messages[len(a.messages)-1]
}

// Messages returns the current message history.
func (a *Agent) Messages() []AgentMessage { return a.messages }

// MessagesSnapshot copies the message history. Unlike Messages it is safe to
// call from any goroutine while a run appends.
func (a *Agent) MessagesSnapshot() []AgentMessage {
	a.messagesMu.RLock()
	defer a.messagesMu.RUnlock()
	return slices.Clone(a.messages)
}

// setMessages replaces the message history under messagesMu.
func (a *Agent) setMessages(msgs []AgentMessage) {
	a.messagesMu.Lock()
	a.messages = msgs
	a.messagesMu.Unlock()
}

// appendMessages extends the message history under messagesMu.
func (a *Agent) appendMessages(msgs ...AgentMessage) {
	a.messagesMu.Lock()
	a.messages = append(a.messages, msgs...)
	a.messagesMu.Unlock()
}

// SystemPrompt returns the current replayed instructions or an explicit provider prompt override. It is safe to call while the agent appends messages.
func (a *Agent) SystemPrompt() string {
	prompt, _ := a.SystemPromptSnapshot()
	return prompt
}

// SystemPromptSnapshot reads the prompt and whether it is present under one lock. Presence distinguishes an empty projected prompt from an uninitialized transcript.
func (a *Agent) SystemPromptSnapshot() (string, bool) {
	a.messagesMu.RLock()
	defer a.messagesMu.RUnlock()
	if a.forcedSystemPrompt != nil {
		return *a.forcedSystemPrompt, true
	}
	systems := systemMessages(a.messages)
	return ai.GetCurrentSystemPrompt(systems), len(systems) > 0
}

// SetMessages replaces the message history (used for session restore). The
// agent keeps a copy of the top-level slice, as upstream's state.messages
// setter copies the assigned array.
func (a *Agent) SetMessages(msgs []AgentMessage) { a.setMessages(slices.Clone(msgs)) }

// SetSessionID updates the session ID used for prompt caching.
// Called after session creation/resume when the stable session ID is known.
func (a *Agent) SetSessionID(id string) { a.opts.SessionID = id }

// SetModel swaps the active LLM model. The next streaming turn uses
// the new provider/model. The agent's tools, system prompt, and
// message history are unchanged. Mid-session model switch.
func (a *Agent) SetModel(m *ai.Model) {
	a.stateMu.Lock()
	a.opts.Model = m
	a.stateRevision++
	a.stateMu.Unlock()
}

// SetBeforeProviderHook transforms each provider's final wire payload.
// Mirrors upstream before_provider_request through StreamOptions.OnPayload.
func (a *Agent) SetBeforeProviderHook(fn func(payload any, model *ai.Model) (any, error)) {
	a.beforeProviderHook = fn
}

// Ports packages/agent/src/agent.ts
// StreamFunction returns the caller's stream override. Nil denotes the stock stream, allowing summarization to distinguish optional custom-stream auth from required stock auth without comparing Go function values.
func (a *Agent) StreamFunction() StreamFn {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.opts.StreamFn
}

// SetStreamFunction replaces the provider request function for subsequent requests, matching Agent.streamFunction assignment in Pi. Nil restores the stock stream.
func (a *Agent) SetStreamFunction(streamFn StreamFn) {
	a.stateMu.Lock()
	a.opts.StreamFn = streamFn
	a.stateMu.Unlock()
}

// SetTransformHeaders sets the hook that rewrites each provider request's
// merged HTTP headers. Mirrors upstream StreamOptions.transformHeaders.
func (a *Agent) SetTransformHeaders(fn func(context.Context, ai.ProviderHeaders) (ai.ProviderHeaders, error)) {
	a.transformHeaders = fn
}

// SetTransformContext sets a hook that fires before each LLM call to allow
// extensions to modify the message context. Mirrors upstream context event.
func (a *Agent) SetTransformContext(fn func(msgs []AgentMessage) []AgentMessage) {
	if fn == nil {
		a.transformContext = nil
		return
	}
	a.transformContext = func(_ context.Context, messages []AgentMessage) ([]AgentMessage, error) {
		return fn(messages), nil
	}
}

// SetTransformContextWithContext installs a request transform with cancellation
// and error propagation owned by the active agent run.
func (a *Agent) SetTransformContextWithContext(fn func(context.Context, []AgentMessage) ([]AgentMessage, error)) {
	a.transformContext = fn
}

// Model returns the active model. New agents use the upstream unknown
// descriptor until one is selected; SetModel(nil) clears it.
func (a *Agent) Model() *ai.Model {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.opts.Model
}

// ThinkingLevel returns the reasoning depth used for future Send calls.
func (a *Agent) ThinkingLevel() ai.ThinkingLevel {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.opts.ThinkingLevel
}

// SetThinkingLevel updates the reasoning depth used for future Send calls.
// Wired by InteractiveMode.cycleThinkingLevel() on Shift+Tab.
// The current in-flight call (if any) is unaffected; the new level applies
// starting with the next agent.Send invocation.
func (a *Agent) SetThinkingLevel(level ai.ThinkingLevel) {
	a.stateMu.Lock()
	a.opts.ThinkingLevel = level
	a.stateRevision++
	a.stateMu.Unlock()
}

// SetTransport updates the preferred provider transport for future Send calls.
func (a *Agent) SetTransport(transport ai.Transport) { a.opts.Transport = transport }

// IsStreaming returns true when the agent is actively in a Send/Continue loop.
// Mirrors upstream AgentState.isStreaming for extension queries.
func (a *Agent) IsStreaming() bool {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return a.streaming
}

// ─── Steering / Follow-up queues ──────────────────────────────────────────────
// Mirrors upstream packages/agent/src/agent.ts:252-279.

// Steer enqueues a steering message. Steering messages are delivered
// after the current tool batch finishes but before the next LLM call,
// allowing the user to redirect the agent mid-turn.
func (a *Agent) Steer(msg AgentMessage) { a.steeringQueue.Enqueue(msg) }

// FollowUp enqueues a follow-up message. Follow-up messages are delivered
// only after the agent has no more tool calls AND no steering messages,
// starting a fresh outer-loop iteration.
func (a *Agent) FollowUp(msg AgentMessage) { a.followUpQueue.Enqueue(msg) }

// QueueNextTurn defers a message to the start of the next user turn, where it
// is injected beside the user message as context for the first model call.
//
// This is not FollowUp. A follow-up continues the turn that is running, so it
// reaches the model only after the current response completes; a message meant
// to inform the next prompt would arrive a model call too late, and on an idle
// agent it would sit in a queue with no goroutine to drain it.
func (a *Agent) QueueNextTurn(msg AgentMessage) {
	a.pendingNextTurnMu.Lock()
	defer a.pendingNextTurnMu.Unlock()
	a.pendingNextTurn = append(a.pendingNextTurn, msg)
}

// takePendingNextTurn removes and returns the deferred messages.
func (a *Agent) takePendingNextTurn() []AgentMessage {
	a.pendingNextTurnMu.Lock()
	defer a.pendingNextTurnMu.Unlock()
	msgs := a.pendingNextTurn
	a.pendingNextTurn = nil
	return msgs
}

// ClearSteeringQueue removes and returns all pending steering messages.
func (a *Agent) ClearSteeringQueue() []AgentMessage { return a.steeringQueue.Clear() }

// ClearFollowUpQueue removes and returns all pending follow-up messages.
func (a *Agent) ClearFollowUpQueue() []AgentMessage { return a.followUpQueue.Clear() }

// HasQueuedMessages reports whether either queue still contains pending
// messages. Mirrors upstream agent.ts:290.
func (a *Agent) HasQueuedMessages() bool {
	return a.steeringQueue.HasItems() || a.followUpQueue.HasItems()
}

// ClearAllQueues removes and returns all pending messages from both queues.
func (a *Agent) ClearAllQueues() (steering, followUp []AgentMessage) {
	return a.steeringQueue.Clear(), a.followUpQueue.Clear()
}

// PeekQueuedMessages previews the messages selected for the next turn without
// consuming them: the steering queue's selection, or the follow-up queue's
// when no steering is queued. Mirrors upstream Agent.peekQueuedMessages.
func (a *Agent) PeekQueuedMessages() []AgentMessage {
	if steering := a.steeringQueue.Peek(); len(steering) > 0 {
		return steering
	}
	return a.followUpQueue.Peek()
}

// HasPendingMessages reports whether either queue has messages.
func (a *Agent) HasPendingMessages() bool {
	return a.steeringQueue.HasItems() || a.followUpQueue.HasItems()
}

// PendingMessages returns a snapshot of both queues without draining.
// Used for UI display (e.g. "2 steering, 1 follow-up pending").
func (a *Agent) PendingMessages() (steering, followUp []AgentMessage) {
	return a.steeringQueue.Messages(), a.followUpQueue.Messages()
}

// SetSteeringMode changes the steering queue's drain policy.
func (a *Agent) SetSteeringMode(mode QueueMode) { a.steeringQueue.SetMode(mode) }

// SetFollowUpMode changes the follow-up queue's drain policy.
func (a *Agent) SetFollowUpMode(mode QueueMode) { a.followUpQueue.SetMode(mode) }

// SteeringMode returns the current steering drain policy.
func (a *Agent) SteeringMode() QueueMode { return a.steeringQueue.Mode() }

// FollowUpMode returns the current follow-up drain policy.
func (a *Agent) FollowUpMode() QueueMode { return a.followUpQueue.Mode() }

// Reset retains the replayed system baseline and clears conversation state and queues. It refuses while a run is
// active, as upstream Agent.reset throws. Mirrors upstream Agent.reset.
func (a *Agent) Reset() error {
	a.stateMu.RLock()
	streaming := a.streaming
	a.stateMu.RUnlock()
	if streaming {
		return fmt.Errorf("%w Wait for completion before resetting.", ErrAlreadyProcessing)
	}
	baseline := ai.GetCurrentSystemMessage(systemMessages(a.messages))
	var messages []AgentMessage
	if baseline != nil {
		messages = []AgentMessage{{System: baseline}}
	}
	a.setMessages(messages)
	a.stateMu.Lock()
	a.streamingMessage = nil
	a.pendingToolCalls = nil
	a.errorMessage = ""
	a.stateMu.Unlock()
	a.steeringQueue.Clear()
	a.followUpQueue.Clear()
	return nil
}

// SetFinishTurn replaces the FinishTurn hook for later runs. Mirrors assigning
// upstream's public Agent.finishTurn; FinishTurnHook returns the current one
// so a caller can chain it.
func (a *Agent) SetFinishTurn(fn FinishTurn) { a.opts.FinishTurn = fn }

// FinishTurnHook returns the current FinishTurn hook, or nil.
func (a *Agent) FinishTurnHook() FinishTurn { return a.opts.FinishTurn }

// SetPrepareRequest replaces the PrepareRequest hook for later runs. Mirrors
// assigning upstream's public Agent.prepareRequest.
func (a *Agent) SetPrepareRequest(fn PrepareRequest) { a.opts.PrepareRequest = fn }

// PrepareRequestHook returns the current PrepareRequest hook, or nil.
func (a *Agent) PrepareRequestHook() PrepareRequest { return a.opts.PrepareRequest }

// SetPrepareNextTurn replaces the PrepareNextTurn hook for later runs. Mirrors
// assigning upstream's public Agent.prepareNextTurnWithContext.
func (a *Agent) SetPrepareNextTurn(fn PrepareNextTurn) { a.opts.PrepareNextTurn = fn }

// PrepareNextTurnHook returns the current PrepareNextTurn hook, or nil.
func (a *Agent) PrepareNextTurnHook() PrepareNextTurn { return a.opts.PrepareNextTurn }

// ToolExecutionMode returns the batch strategy for assistant messages with several tool calls. Mirrors upstream Agent.toolExecution.
func (a *Agent) ToolExecutionMode() ToolExecutionMode { return a.opts.ToolExecution }

// AddBeforeToolCallHook appends a hook that fires before each tool execution.
// Mirrors upstream agent.beforeToolCall assignment (agent-session.ts:373).
func (a *Agent) AddBeforeToolCallHook(h BeforeToolCallHook) {
	a.opts.BeforeToolCall = append(a.opts.BeforeToolCall, h)
}

// AddAfterToolCallHook appends a hook that fires after each tool execution.
// Mirrors upstream agent.afterToolCall assignment (agent-session.ts:396).
func (a *Agent) AddAfterToolCallHook(h AfterToolCallHook) {
	a.opts.AfterToolCall = append(a.opts.AfterToolCall, h)
}

// persistMessage invokes the OnMessagePersist hook for a newly produced
// message. Driven by message_end in emit(), mirroring upstream's single
// persistence site (agent-session.ts:511-525). System, user, assistant, toolResult and
// custom messages persist here; bash-execution entries keep their own append
// path.
//
// Custom messages are included because an extension can queue one with
// deliverAs steer/followUp while a turn is in flight. Persisting at creation
// instead would record it between an assistant's tool_use and its tool_result,
// which replays as an invalid sequence ("`tool_use` ids were found without
// `tool_result` blocks immediately after").
func (a *Agent) persistMessage(msg AgentMessage) error {
	if a.opts.OnMessagePersist == nil {
		return nil
	}
	if msg.System == nil && msg.User == nil && msg.Assistant == nil && msg.ToolResult == nil && msg.Custom == nil {
		return nil
	}
	return a.opts.OnMessagePersist(msg)
}

// runFailure unwinds the agent loop when handling an event fails, as a
// throwing listener rejects upstream Agent.processEvents and ends the run.
type runFailure struct{ err error }

// catchRunFailure runs fn on the loop goroutine and returns the handler
// failure that unwound it, if any. Other panics propagate.
func catchRunFailure(fn func() error) (runErr, failure error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			raised, ok := recovered.(runFailure)
			if !ok {
				panic(recovered)
			}
			failure = raised.err
		}
	}()
	return fn(), nil
}

// handleRunFailure ends a run that a failing event handler interrupted with an
// error assistant message, turn_end, and agent_end, and returns the failure
// that interrupted delivering them. Mirrors upstream Agent.handleRunFailure.
func (a *Agent) handleRunFailure(err error, aborted bool, model *ai.Model) error {
	stopReason := ai.StopReasonError
	if aborted {
		stopReason = ai.StopReasonAborted
	}
	message := &AssistantMessage{
		Role:         RoleAssistant,
		Content:      []ai.AssistantContentBlock{ai.TextContent{Text: ""}},
		Usage:        &ai.Usage{},
		StopReason:   stopReason,
		ErrorMessage: err.Error(),
		Timestamp:    time.Now().UnixMilli(),
	}
	if model != nil {
		message.ModelID = model.ID
		if model.Provider != nil {
			message.Provider = model.Provider.ID()
		}
	}
	failureMessage := AgentMessage{Assistant: message}
	_, failure := catchRunFailure(func() error {
		a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
		a.emit(MessageEndEvent{Message: failureMessage})
		a.emit(TurnEndEvent{Message: failureMessage})
		a.emit(AgentEndEvent{Messages: []AgentMessage{failureMessage}})
		return nil
	})
	return failure
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

func (a *Agent) emit(ev AgentEvent) {
	// message_end commits to public state before listeners run. The provider
	// context is separate, so unfinished or future prompt messages stay hidden.
	if end, ok := ev.(MessageEndEvent); ok {
		a.appendMessages(end.Message)
	}
	a.reduceEventState(ev)
	a.notifyListeners(ev)
	if a.opts.OnEvent != nil {
		a.opts.OnEvent(ev)
	}
	// Persist each completed message once. Prompt, steering and follow-up messages emit message_end when admitted; resumed history does not replay its lifecycle.
	var persistErr error
	if me, ok := ev.(MessageEndEvent); ok {
		persistErr = a.persistMessage(me.Message)
	}
	if a.opts.EventCh != nil {
		select {
		case a.opts.EventCh <- ev:
		case <-a.opts.EventDone:
		}
	}
	if persistErr != nil {
		panic(runFailure{err: persistErr})
	}
	if a.opts.AfterEvent != nil {
		if err := a.opts.AfterEvent(ev); err != nil {
			panic(runFailure{err: err})
		}
	}
}

// toolCallArguments is intentionally a byte slice rather than strings.Builder.
// pendingToolCall values are copied while a batch is prepared and finalized;
// copying a non-zero strings.Builder is invalid and its copyCheck panics when
// a later caller appends to the copied value.
type toolCallArguments []byte

func (a *toolCallArguments) Write(p []byte) (int, error) {
	*a = append(*a, p...)
	return len(p), nil
}

func (a *toolCallArguments) WriteString(s string) (int, error) {
	*a = append(*a, s...)
	return len(s), nil
}

func (a toolCallArguments) String() string { return string(a) }

type pendingToolCall struct {
	id               string
	name             string
	args             toolCallArguments
	thoughtSignature string // encrypted reasoning context for replay
}

// consumeStream drives one provider response into agent events. thinking is the level the request asked for; it is recorded on the final response, whichever stream function answered (upstream agent-loop.ts:408-409). Partial messages do not carry it.
func (a *Agent) consumeStream(ctx context.Context, stream *ai.AssistantMessageEventStream, model *ai.Model, thinking ai.ThinkingLevel) (*AssistantMessage, []pendingToolCall, error) {
	thinking = recordedThinkingLevel(thinking)
	result := func(final *ai.AssistantMessage) *AssistantMessage {
		message := agentAssistantMessage(final)
		message.ThinkingLevel = thinking
		return message
	}
	var message *AssistantMessage
	started := false

	caller := ai.StreamObservationFromContext(ctx)
	iteratorContext := ctx
	if signal, ok := ctx.(*runDeadlineContext); ok {
		// Agent.abort waits for the provider's terminal response, preserving its
		// error and draining its owned work. The Go caller can still cancel its
		// own context if it must stop waiting for a non-responsive provider.
		iteratorContext = signal.parent
	}
	for observation, event := range stream.ObserveEvents(caller.Context(iteratorContext)) {
		if !started {
			switch event.(type) {
			case ai.StartEvent, ai.DoneEvent, ai.ErrorEvent:
			default:
				continue
			}
		}
		switch event := event.(type) {
		case ai.StartEvent:
			message = shallowAssistantMessage(event.Partial)
			started = true
			a.emitObserved(observation, MessageStartEvent{Message: AgentMessage{Assistant: message}})
		case ai.TextStartEvent:
			message = a.emitAssistantUpdate(observation, event.Partial, event)
		case ai.TextDeltaEvent:
			message = a.emitAssistantUpdate(observation, event.Partial, event)
		case ai.TextEndEvent:
			message = a.emitAssistantUpdate(observation, event.Partial, event)
		case ai.ThinkingStartEvent:
			message = a.emitAssistantUpdate(observation, event.Partial, event)
		case ai.ThinkingDeltaEvent:
			message = a.emitAssistantUpdate(observation, event.Partial, event)
		case ai.ThinkingEndEvent:
			message = a.emitAssistantUpdate(observation, event.Partial, event)
		case ai.ToolCallStartEvent:
			message = a.emitAssistantUpdate(observation, event.Partial, event)
		case ai.ToolCallDeltaEvent:
			message = a.emitAssistantUpdate(observation, event.Partial, event)
		case ai.ToolCallEndEvent:
			message = a.emitAssistantUpdate(observation, event.Partial, event)
		case ai.DoneEvent:
			message = result(stream.Result())
			awaitResultWrapper(observation)
			if !started {
				a.emitObserved(observation, MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
			}
			message = a.endAssistantMessage(observation, message)
			return message, pendingToolCalls(message), nil
		case ai.ErrorEvent:
			message = result(stream.Result())
			awaitResultWrapper(observation)
			if !started {
				a.emitObserved(observation, MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
			}
			return a.endAssistantMessage(observation, message), nil, nil
		}
	}

	final, resultErr := stream.ResultContext(ctx)
	if ctx.Err() != nil || resultErr != nil {
		if message == nil {
			providerID, modelID := "", ""
			if model != nil {
				modelID = model.ID
				if model.Provider != nil {
					providerID = model.Provider.ID()
				}
			}
			message = result(&ai.AssistantMessage{
				Provider: providerID, Model: modelID,
				StopReason: ai.StopReasonAborted, Timestamp: time.Now().UnixMilli(),
			})
		} else {
			message = message.Observe()
			message.StopReason = ai.StopReasonAborted
			message.ThinkingLevel = thinking
		}
		if !started {
			a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
		}
		return a.endAssistantMessage(nil, message), nil, ctx.Err()
	}

	finished := result(final)
	if !started {
		a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(finished)}})
	}
	finished = a.endAssistantMessage(nil, finished)
	return finished, pendingToolCalls(finished), nil
}

// awaitResultWrapper is the reaction that upstream's `result` wrapper adds to `await response.result()` (packages/agent/src/agent-loop.ts:409 `const result = async () => Object.assign(await response.result(), ...)`, awaited at :445 and :460): the wrapper's own promise settles one reaction after the inner await.
func awaitResultWrapper(observation *ai.StreamObservation) {
	if observation != nil {
		observation.Yield()
	}
}

// recordedThinkingLevel is the thinkingLevel a final response records for a request that asked for level (upstream agent-loop.ts:409, `config.reasoning ?? "off"`).
func recordedThinkingLevel(level ai.ThinkingLevel) ai.ThinkingLevel {
	if level == "" {
		return ai.ThinkingOff
	}
	return level
}

// endAssistantMessage emits message_end for a finalized assistant response and
// returns the message the transcript records. The event and the transcript
// share it, as upstream shares one object, so an OnEvent replacement applied
// in place reaches agent state, persistence and listeners alike.
func (a *Agent) endAssistantMessage(observation *ai.StreamObservation, message *AssistantMessage) *AssistantMessage {
	final := cloneAssistantMessage(message)
	a.emitObserved(observation, MessageEndEvent{Message: AgentMessage{Assistant: final}})
	return final
}

func (a *Agent) emitAssistantUpdate(observation *ai.StreamObservation, partial *ai.AssistantMessage, event ai.AssistantMessageEvent) *AssistantMessage {
	message := shallowAssistantMessage(partial)
	a.emitObserved(observation, MessageUpdateEvent{
		Message:               AgentMessage{Assistant: message},
		AssistantMessageEvent: event,
	})
	return message
}

// upstream: packages/agent/src/agent-loop.ts:416-453 awaits the event sink even when its synchronous prefix completes without blocking.
func (a *Agent) emitObserved(observation *ai.StreamObservation, event AgentEvent) {
	a.emit(WithEventObservation(event, observation))
	if observation != nil {
		observation.Yield()
	}
}

// upstream: packages/agent/src/agent-loop.ts:408-433
func shallowAssistantMessage(partial *ai.AssistantMessage) *AssistantMessage {
	view := partial.ShallowCopy()
	message := agentAssistantMessage(view.Observe())
	message.streamView = view
	return message
}

func agentAssistantMessage(message *ai.AssistantMessage) *AssistantMessage {
	if message == nil {
		return &AssistantMessage{Role: RoleAssistant}
	}
	usage := message.Usage
	out := &AssistantMessage{
		Role:                  RoleAssistant,
		Content:               append([]ai.AssistantContentBlock(nil), message.Content...),
		Timestamp:             message.Timestamp,
		Usage:                 &usage,
		API:                   message.API,
		Provider:              message.Provider,
		ModelID:               message.Model,
		ResponseModel:         message.ResponseModel,
		ResponseID:            message.ResponseID,
		ProviderThinkingLevel: message.ProviderThinkingLevel,
		ThinkingLevel:         message.ThinkingLevel,
		Diagnostics:           append([]ai.AssistantMessageDiagnostic(nil), message.Diagnostics...),
		Deferred:              message.Deferred,
		StopReason:            message.StopReason,
		ErrorMessage:          message.ErrorMessage,
		RawStopReason:         message.RawStopReason,
		EndTurn:               message.EndTurn,
	}
	for _, block := range message.Content {
		if thinking, ok := block.(ai.ThinkingContent); ok {
			out.Thinking += thinking.Thinking
			out.ThinkingSignature = thinking.ThinkingSignature
		}
	}
	return out
}

func newPendingToolCall(call ai.ToolCall) pendingToolCall {
	arguments, err := call.ArgumentsJSON()
	if err != nil {
		arguments = []byte("{}")
	}
	pending := pendingToolCall{id: call.ID, name: call.Name, thoughtSignature: call.ThoughtSignature}
	pending.args = append(pending.args, arguments...)
	return pending
}

func pendingToolCalls(message *AssistantMessage) []pendingToolCall {
	calls := make([]pendingToolCall, 0)
	for _, block := range message.Content {
		call, ok := block.(ai.ToolCall)
		if !ok {
			continue
		}
		pending := newPendingToolCall(call)
		calls = append(calls, pending)
	}
	return calls
}

// cloneAssistantMessage copies a finished or synthesized message for a lifecycle event. Its producer no longer mutates it, so an owned copy reads as Pi's object spread does.
func cloneAssistantMessage(msg *AssistantMessage) *AssistantMessage {
	if msg == nil {
		return nil
	}
	msg = msg.Observe()
	cp := *msg
	cp.Content = append([]ai.AssistantContentBlock(nil), msg.Content...)
	if msg.EndTurn != nil {
		cp.EndTurn = new(*msg.EndTurn)
	}
	return &cp
}
