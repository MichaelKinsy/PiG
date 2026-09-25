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

// UserMessage is a message from the user.
type UserMessage struct {
	Role      string                `json:"role"` // "user"
	Content   []ai.UserContentBlock `json:"content"`
	Timestamp int64                 `json:"timestamp"`
}

// AssistantMessage is a streamed response from the LLM.
type AssistantMessage struct {
	Role                  string                          `json:"role"` // "assistant"
	Content               []ai.AssistantContentBlock      `json:"content"`
	Thinking              string                          `json:"-"`
	Timestamp             int64                           `json:"timestamp"`
	Usage                 *ai.Usage                       `json:"usage,omitempty"`
	API                   ai.API                          `json:"api,omitempty"`
	Provider              string                          `json:"provider,omitempty"`
	ModelID               string                          `json:"model,omitempty"`
	ResponseModel         string                          `json:"responseModel,omitempty"`
	ResponseID            string                          `json:"responseId,omitempty"`
	ProviderThinkingLevel string                          `json:"providerThinkingLevel,omitempty"`
	Diagnostics           []ai.AssistantMessageDiagnostic `json:"diagnostics,omitempty"`
	Deferred              *ai.DeferredHandle              `json:"deferred,omitempty"`
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
}

// ToolResultMessage carries one tool execution result back to the LLM.
// Mirrors upstream pi-ai ToolResultMessage.
type ToolResultMessage struct {
	Role       string                        `json:"role"` // "toolResult"
	ToolCallID string                        `json:"toolCallId"`
	ToolName   string                        `json:"toolName"`
	Content    []ai.ToolResultMessageContent `json:"content"`
	Details    any                           `json:"details,omitempty"`
	Usage      *ai.Usage                     `json:"usage,omitempty"`
	IsError    bool                          `json:"isError"`
	Timestamp  int64                         `json:"timestamp"`
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
		blocks = make([]ai.ContentBlock, len(m.User.Content))
		for i, block := range m.User.Content {
			blocks[i] = block
		}
	case m.Assistant != nil:
		blocks = make([]ai.ContentBlock, len(m.Assistant.Content))
		for i, block := range m.Assistant.Content {
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

// ToolUpdateCallback is called when a tool emits a streaming progress update.
type ToolUpdateCallback func(content string, details any)

// ToolExecutionMode controls parallelism.
type ToolExecutionMode string

const (
	ToolModeSequential ToolExecutionMode = "sequential"
	ToolModeParallel   ToolExecutionMode = "parallel"
)

// AgentToolResult is the result of a tool execution.
//
// upstream: agent/src/types.ts AgentToolResult: content is
// `(TextContent | ImageContent)[]`. In pig, Content (string) is the
// primary text-only path; Images carries optional image blocks for
// multi-modal results (e.g. read tool on image files).
type AgentToolResult struct {
	Content string
	// Images holds optional image content returned alongside text.
	// When non-nil, the provider serialises both Content (as TextContent)
	// and each image (as ImageContent) in the tool result message.
	// upstream: content: (TextContent | ImageContent)[]
	Images  []ai.ImageContent
	Details any
	IsError bool
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
type MessageStartEvent struct{ Message AgentMessage }
type MessageUpdateEvent struct {
	Message               AgentMessage
	AssistantMessageEvent ai.AssistantMessageEvent
}
type MessageEndEvent struct{ Message AgentMessage }
type ToolExecutionStartEvent struct {
	ToolCallID string
	ToolName   string
	ToolLabel  string // human-readable display name (empty = use ToolName)
	Args       json.RawMessage
}
type ToolExecutionUpdateEvent struct {
	// Streaming partial output. Subsequent updates carry the full
	// accumulated content so far (mirrors upstream pi's onUpdate
	// contract: every update is a complete-so-far snapshot, not a
	// delta). Tool components in the TUI replace their visible body
	// with each update, giving a live tail.
	ToolCallID string
	ToolName   string
	Content    string
	Details    any
	// Args is the original tool call arguments (same as ToolExecutionStartEvent.Args).
	// Passed through for extensions that need to correlate updates with inputs.
	Args json.RawMessage
}
type ToolExecutionEndEvent struct {
	ToolCallID string
	ToolName   string
	Result     AgentToolResult
	Duration   time.Duration
}

// TimingEvent is emitted whenever a turn ends or a tool completes so
// status-line / extension / diagnostics consumers can read live timing
// data without needing to subscribe to TurnEnd + ToolExecutionEnd
// individually. Kind is one of "turn", "tool", or "session_end".
type TimingEvent struct {
	Kind     string        // "turn" | "tool" | "session_end"
	Name     string        // tool name when Kind == "tool"; empty otherwise
	Duration time.Duration // for the just-completed turn or tool call
	Snapshot TimingSnapshot
}

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
func (QueueUpdateEvent) agentEvent()          {}
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

func (TimingEvent) agentEvent()                         {}
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
// Merge semantics: field-by-field replace; no deep merge. Nil pointer = no override.
//
//   - Content: if non-nil, replaces the tool result text.
//   - Images: if non-nil, replaces the tool result image blocks.
//   - IsError: if non-nil, replaces the tool result error flag.
//   - Usage: if non-nil, replaces the tool result usage.
//   - Terminate: if non-nil, replaces the early-termination hint. The agent
//     stops after the batch only when every finalized result terminates
//     (upstream shouldTerminateToolBatch).
type AfterToolCallResult struct {
	Content   *string
	Images    *[]ai.ImageContent
	Details   any
	IsError   *bool // nil = keep original
	Usage     *ai.Usage
	Terminate *bool
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
	// StreamFn sends each provider request. Nil uses the model's provider.
	// Mirrors upstream AgentOptions.streamFn.
	StreamFn StreamFn

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
	messagesMu    sync.RWMutex
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
	out := make([]AgentTool, len(a.opts.Tools))
	copy(out, a.opts.Tools)
	return out
}

// SetTools replaces the active tool list for subsequent turns.
func (a *Agent) SetTools(tools []AgentTool) {
	a.opts.Tools = append([]AgentTool(nil), tools...)
}

// SetSystemPrompt projects a replacement system prompt onto subsequent provider requests without rewriting transcript history.
func (a *Agent) SetSystemPrompt(prompt string) {
	a.opts.SystemPrompt = prompt
	a.forcedSystemPrompt = new(prompt)
}

// ClearSystemPrompt removes a SetSystemPrompt projection, so later requests
// use the transcript's own system prompt again.
func (a *Agent) ClearSystemPrompt() {
	a.forcedSystemPrompt = nil
}

// ErrNoModelSelected is returned when a turn is requested with no usable model.
// Upstream agent-session.prompt() throws formatNoModelSelectedMessage() before
// streaming; pig surfaces the same condition as an error rather than panicking
// on a nil model in runLoop.
var ErrNoModelSelected = errors.New("no model selected")

// ErrAlreadyProcessingPrompt is returned when a prompt is sent while a run is
// active. Mirrors upstream Agent.prompt's "Agent is already processing a
// prompt" error: queue the message with Steer or FollowUp instead.
var ErrAlreadyProcessingPrompt = errors.New("agent is already processing a prompt; use Steer or FollowUp to queue messages, or wait for completion")

// ErrAlreadyProcessing is returned when Continue or Reset is called while a
// run is active. Mirrors upstream Agent.continue and Agent.reset.
var ErrAlreadyProcessing = errors.New("agent is already processing; wait for completion")

// ErrNoMessagesToContinue is returned by Continue on an empty transcript.
// Mirrors upstream Agent.continue's "No messages to continue from".
var ErrNoMessagesToContinue = errors.New("no messages to continue from")

// ErrToolResultsUnanswered reports that a run ended with tool results the
// model never answered although no tool asked to terminate the run. It guards
// the invariant that a run never ends silently mid-task.
var ErrToolResultsUnanswered = errors.New("agent run ended after tool results without a model response")

// ErrMaxTurnsReached reports that a run stopped at AgentOptions.MaxTurns with
// tool results still waiting for a model response.
var ErrMaxTurnsReached = errors.New("agent stopped at its turn limit with tool results still unanswered")

// ensureModel reports whether the agent has a usable model+provider to stream
// with. Callers reject the turn instead of dereferencing a nil model.
func (a *Agent) ensureModel() error {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	if a.opts.Model == nil || a.opts.Model.Provider == nil {
		return ErrNoModelSelected
	}
	return nil
}

// beginRun claims the agent for one run, or returns busy when a run is active.
// Mirrors upstream Agent's activeRun guard.
func (a *Agent) beginRun(busy error) error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.streaming {
		return busy
	}
	a.streaming = true
	return nil
}

// finishRun releases the claim beginRun took.
func (a *Agent) finishRun() {
	a.stateMu.Lock()
	a.streaming = false
	a.stateMu.Unlock()
}

// Send sends a plain-text user message and runs the agent loop until the LLM
// stops calling tools. Steering and follow-up messages queued via
// Steer/FollowUp are polled at the appropriate points in the loop.
func (a *Agent) Send(ctx context.Context, content string) ([]AgentMessage, error) {
	return a.SendContent(ctx, []ai.UserContentBlock{ai.TextContent{Text: content}})
}

// SendContent is the structured-content variant of Send.
func (a *Agent) SendContent(ctx context.Context, content []ai.UserContentBlock) ([]AgentMessage, error) {
	if err := a.beginRun(ErrAlreadyProcessingPrompt); err != nil {
		return nil, err
	}
	defer a.finishRun()
	// Reject before appending the user message so a rejected turn leaves history
	// unchanged (mirrors upstream prompt() validating the model first).
	if err := a.ensureModel(); err != nil {
		return nil, err
	}
	userMsg := AgentMessage{
		User: &UserMessage{
			Role:      RoleUser,
			Content:   append([]ai.UserContentBlock(nil), content...),
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
	return a.runPromptMessages(ctx, msgs, a.createLoopConfig(false))
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
	if err := a.beginRun(ErrAlreadyProcessingPrompt); err != nil {
		return nil, err
	}
	defer a.finishRun()
	// Validate before appending so a rejected turn leaves history unchanged,
	// matching SendContent.
	if err := a.ensureModel(); err != nil {
		return nil, err
	}
	if a.opts.PreparePrompt != nil {
		var err error
		msgs, err = a.opts.PreparePrompt(ctx, msgs)
		if err != nil {
			return nil, err
		}
	}
	return a.runPromptMessages(ctx, msgs, a.createLoopConfig(false))
}

// Continue runs the agent loop from the current message state without
// prepending a new user message. Used to retry a turn after auto-compaction
// when an overflow error is recovered by compacting and replaying.
//
// If the last message is an assistant message, Continue first checks the
// steering queue, then the follow-up queue, before falling through to
// runLoop. Mirrors upstream Agent.continue (packages/agent/src/agent.ts).
func (a *Agent) Continue(ctx context.Context) ([]AgentMessage, error) {
	if err := a.beginRun(ErrAlreadyProcessing); err != nil {
		return nil, err
	}
	defer a.finishRun()
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
		return a.messages, fmt.Errorf("cannot continue from message role: assistant")
	}
	return a.runLoop(ctx, a.createLoopConfig(false), len(a.messages))
}

// runPromptMessages appends a run's prompt messages and runs the loop, which
// replays their lifecycle events. Mirrors upstream Agent.runPromptMessages.
func (a *Agent) runPromptMessages(ctx context.Context, msgs []AgentMessage, cfg agentLoopConfig) ([]AgentMessage, error) {
	runStart := len(a.messages)
	a.appendMessages(a.declareToolChanges(a.messages, msgs)...)
	return a.runLoop(ctx, cfg, runStart)
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

// agentEndMessages returns a snapshot of the message history for an
// AgentEndEvent. The agent keeps appending to a.messages on retry or
// continuation after agent_end fires, while consumers read the event's
// Messages on other goroutines (e.g. the session's forwardAgentEvents loop
// computing willRetry and dispatching to extensions). Sharing the live slice
// races the loop's append, so each agent_end carries its own copy. The copy
// runs on the agent goroutine at emit time, so it never races the append.
func (a *Agent) agentEndMessages() []AgentMessage {
	return append([]AgentMessage(nil), a.messages...)
}

// SystemPrompt returns the current replayed instructions or an explicit provider prompt override.
func (a *Agent) SystemPrompt() string {
	if a.forcedSystemPrompt != nil {
		return *a.forcedSystemPrompt
	}
	return ai.GetCurrentSystemPrompt(systemMessages(a.messages))
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

// Model returns the currently active model. May be nil if the agent
// was constructed without one.
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
		return ErrAlreadyProcessing
	}
	baseline := ai.GetCurrentSystemMessage(systemMessages(a.messages))
	var messages []AgentMessage
	if baseline != nil {
		messages = []AgentMessage{{System: baseline}}
	}
	a.setMessages(messages)
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
	// Upstream appends from its single event-loop owner; Go takes messagesMu so
	// concurrent MessagesSnapshot readers (the cache warmer) stay race-free.
	a.appendMessages(failureMessage)
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
	if a.opts.OnEvent != nil {
		a.opts.OnEvent(ev)
	}
	// Persist on message_end, mirroring upstream agent-session.ts:511-525:
	// every user/assistant/toolResult message is persisted exactly once as the
	// agent loop emits it. The runLoop replay (a.messages[runStart:]) emits
	// message_end only for THIS run's new messages (prompt, steering,
	// follow-up): never resumed history: so each message persists once.
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

func (a *Agent) consumeStream(ctx context.Context, stream *ai.AssistantMessageEventStream, model *ai.Model) (*AssistantMessage, []pendingToolCall, error) {
	var message *AssistantMessage
	started := false

	for event := range stream.Events(ctx) {
		switch event := event.(type) {
		case ai.StartEvent:
			message = agentAssistantMessage(event.Partial)
			started = true
			a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
		case ai.TextStartEvent:
			message = a.emitAssistantUpdate(event.Partial, event)
		case ai.TextDeltaEvent:
			message = a.emitAssistantUpdate(event.Partial, event)
		case ai.TextEndEvent:
			message = a.emitAssistantUpdate(event.Partial, event)
		case ai.ThinkingStartEvent:
			message = a.emitAssistantUpdate(event.Partial, event)
		case ai.ThinkingDeltaEvent:
			message = a.emitAssistantUpdate(event.Partial, event)
		case ai.ThinkingEndEvent:
			message = a.emitAssistantUpdate(event.Partial, event)
		case ai.ToolCallStartEvent:
			message = a.emitAssistantUpdate(event.Partial, event)
		case ai.ToolCallDeltaEvent:
			message = a.emitAssistantUpdate(event.Partial, event)
		case ai.ToolCallEndEvent:
			message = a.emitAssistantUpdate(event.Partial, event)
		case ai.DoneEvent:
			message = agentAssistantMessage(event.Message)
			if !started {
				a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
			}
			message = a.endAssistantMessage(message)
			return message, pendingToolCalls(message), nil
		case ai.ErrorEvent:
			message = agentAssistantMessage(event.Error)
			if !started {
				a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
			}
			return a.endAssistantMessage(message), nil, nil
		}
	}

	if ctx.Err() != nil {
		if message == nil {
			providerID, modelID := "", ""
			if model != nil {
				modelID = model.ID
				if model.Provider != nil {
					providerID = model.Provider.ID()
				}
			}
			message = agentAssistantMessage(&ai.AssistantMessage{
				Provider: providerID, Model: modelID,
				StopReason: ai.StopReasonAborted, Timestamp: time.Now().UnixMilli(),
			})
		} else {
			message.StopReason = ai.StopReasonAborted
		}
		if !started {
			a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(message)}})
		}
		return a.endAssistantMessage(message), nil, ctx.Err()
	}

	result := agentAssistantMessage(stream.Result())
	if !started {
		a.emit(MessageStartEvent{Message: AgentMessage{Assistant: cloneAssistantMessage(result)}})
	}
	result = a.endAssistantMessage(result)
	return result, pendingToolCalls(result), nil
}

// endAssistantMessage emits message_end for a finalized assistant response and
// returns the message the transcript records. The event and the transcript
// share it, as upstream shares one object, so an OnEvent replacement applied
// in place reaches agent state, persistence and listeners alike.
func (a *Agent) endAssistantMessage(message *AssistantMessage) *AssistantMessage {
	final := cloneAssistantMessage(message)
	a.emit(MessageEndEvent{Message: AgentMessage{Assistant: final}})
	return final
}

func (a *Agent) emitAssistantUpdate(partial *ai.AssistantMessage, event ai.AssistantMessageEvent) *AssistantMessage {
	message := agentAssistantMessage(partial)
	a.emit(MessageUpdateEvent{
		Message:               AgentMessage{Assistant: cloneAssistantMessage(message)},
		AssistantMessageEvent: event,
	})
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

func pendingToolCalls(message *AssistantMessage) []pendingToolCall {
	calls := make([]pendingToolCall, 0)
	for _, block := range message.Content {
		call, ok := block.(ai.ToolCall)
		if !ok {
			continue
		}
		arguments, err := json.Marshal(call.Arguments)
		if err != nil {
			arguments = []byte("{}")
		}
		pending := pendingToolCall{
			id: call.ID, name: call.Name, thoughtSignature: call.ThoughtSignature,
		}
		pending.args = append(pending.args, arguments...)
		calls = append(calls, pending)
	}
	return calls
}

func cloneAssistantMessage(msg *AssistantMessage) *AssistantMessage {
	if msg == nil {
		return nil
	}
	cp := *msg
	cp.Content = append([]ai.AssistantContentBlock(nil), msg.Content...)
	if msg.EndTurn != nil {
		cp.EndTurn = new(*msg.EndTurn)
	}
	return &cp
}
