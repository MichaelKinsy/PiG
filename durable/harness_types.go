package durable

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// Ports the subset of packages/durable/src/harness/types.ts that packages/durable/src/types.ts references:
// TaskRuntime and the entry kinds need these declarations, and types.ts and harness/types.ts import each other. Go
// packages cannot import each other, so these declarations live in the root package and durable/harness declares the
// rest of harness/types.ts over them.

// ModelRef is a provider and model ID resolved through pi-ai Models.
type ModelRef struct {
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
}

// UserInput is the content of a user message.
type UserInput = ai.UserContent

// WhenBusy is the busy policy of an input submission.
type WhenBusy string

const (
	WhenBusySteer    WhenBusy = "steer"
	WhenBusyFollowUp WhenBusy = "followUp"
	WhenBusyReject   WhenBusy = "reject"
)

// SubmissionDraft is a host submission: user input (type input, with Content and an optional WhenBusy) that may start
// a run, or a passive entry write (type write, with Entry).
type SubmissionDraft struct {
	// RequestId is nil when absent.
	RequestId *string
	Type      SubmissionType
	Content   UserInput
	WhenBusy  WhenBusy
	Entry     *EntryDraft
}

// InputSubmissionDraft is a SubmissionDraft of type input.
type InputSubmissionDraft = SubmissionDraft

// SettledSubmissionRecord is a SubmissionRecord whose status is done or unanswered.
type SettledSubmissionRecord = SubmissionRecord

// SubmissionAbortResult is the result of aborting one submission.
type SubmissionAbortResult string

const (
	SubmissionAborted       SubmissionAbortResult = "aborted"
	SubmissionAlreadyPlaced SubmissionAbortResult = "already_placed"
	SubmissionSettled       SubmissionAbortResult = "settled"
	SubmissionNotFound      SubmissionAbortResult = "not_found"
)

// Submission is the awaitable host object for one durably admitted submission.
type Submission interface {
	Id() SubmissionId
	Status(ctx context.Context) (SubmissionRecord, error)
	Wait(ctx context.Context) (SettledSubmissionRecord, error)
	Abort(ctx context.Context) (SubmissionAbortResult, error)
}

// SettledTask is a task record in the terminal state.
type SettledTask[R any] = TaskRecord[JsonValue, JsonValue, R]

// ConversationAbortOptions holds the options of Conversation.Abort.
type ConversationAbortOptions struct {
	// Background crosses background boundaries: mark every live task reached ignoring the background flag when the
	// abort is admitted, withdraw the queued inputs of every conversation reached, and wait until those tasks are
	// terminal and the conversation is ordinarily idle. Background work created afterwards is neither marked nor
	// awaited.
	Background bool
}

// ConversationHandle holds the invocation-bound conversation operations for tasks and tools. It fails after the
// invocation ends; passive entries are written with ordinary transaction writes instead.
type ConversationHandle interface {
	Id() ConversationId
	Submit(ctx context.Context, submission InputSubmissionDraft) (Submission, error)
	// Abort is Conversation.Abort: withdraw queued inputs, abort the ordinary ownership scope, and wait until it is
	// idle.
	Abort(ctx context.Context, options *ConversationAbortOptions) error
	// WaitForIdle returns when the conversation's ordinary ownership scope has no live non-background task.
	WaitForIdle(ctx context.Context) error
}

// ToolControl holds the post-tools controls requested by a tool result.
type ToolControl struct {
	// AddTools is nil when absent; an empty non-nil list is written as [].
	AddTools []string `json:"addTools,omitempty"`
	// Terminate is Pi's `terminate?: true`: false is absent.
	Terminate bool `json:"terminate,omitempty"`
	// Handoff is nil when absent.
	Handoff *string `json:"handoff,omitempty"`
}

// MarshalJSON writes the controls as Pi stores them after dropping undefined keys (tool.ts copyJson with
// omitUndefinedProperties): an absent key is omitted, and an empty addTools list is kept.
func (control ToolControl) MarshalJSON() ([]byte, error) {
	var addTools *[]string
	if control.AddTools != nil {
		addTools = &control.AddTools
	}
	return json.Marshal(struct {
		AddTools  *[]string `json:"addTools,omitempty"`
		Terminate bool      `json:"terminate,omitempty"`
		Handoff   *string   `json:"handoff,omitempty"`
	}{addTools, control.Terminate, control.Handoff})
}

// ToolDiagnosticSeverity is "info", "warn", or "error".
type ToolDiagnosticSeverity string

const (
	SeverityInfo  ToolDiagnosticSeverity = "info"
	SeverityWarn  ToolDiagnosticSeverity = "warn"
	SeverityError ToolDiagnosticSeverity = "error"
)

// ToolDiagnostic is a remark about a call for the model and the UI, such as truncation or a spill path; never part of
// the tool's data.
type ToolDiagnostic struct {
	Severity ToolDiagnosticSeverity `json:"severity"`
	Message  string                 `json:"message"`
	Code     string                 `json:"code,omitempty"`
}

// ToolExecutionResult is the result of one tool execution.
type ToolExecutionResult struct {
	// Content is nil when omitted: the retained output() text becomes the content.
	Content []ai.ToolResultMessageContent
	IsError *bool
	// Details is omitted when HasDetails is false: the last details() value becomes the details.
	Details    JsonValue
	HasDetails bool
	// Diagnostics are added after those recorded through api.Diagnostic.
	Diagnostics []ToolDiagnostic
	// Usage is the spend of the execution itself, such as a model call; stored on the result and in pi.usage.tools.
	Usage   *ai.Usage
	Control *ToolControl
}

// ToolExecutionMode is whether the tools of one round run at once or one after another in call order.
type ToolExecutionMode string

const (
	ToolExecutionParallel   ToolExecutionMode = "parallel"
	ToolExecutionSequential ToolExecutionMode = "sequential"
)

// QueueMode is how many queued items of one mode a boundary places: the first, or all of them.
type QueueMode string

const (
	QueueAll        QueueMode = "all"
	QueueOneAtATime QueueMode = "one-at-a-time"
)

// ToolExecutionApi holds the operations available to one tool invocation. Every operation fails after the invocation
// ends.
type ToolExecutionApi interface {
	DocumentObserver
	DocumentReader

	TaskId() TaskId
	ConversationId() ConversationId
	CallId() string
	// Registry is the tool task's phase snapshot.
	Registry() RegistrySnapshot
	// Agent is the calling conversation's agent, as the tool task's phase resolved it.
	Agent(ctx context.Context) (Agent, error)
	// Env is built by HarnessOptions.env for this call; nil without an environment.
	Env() env.ExecutionEnv
	// Output appends running output, a string or []byte; it becomes the result content when the result omits
	// Content.
	Output(chunk any)
	// Diagnostic records a model-visible remark about this call.
	Diagnostic(diagnostic ToolDiagnostic)
	// Details replaces running details; the last value becomes the result details when the result omits Details.
	Details(ctx context.Context, value JsonValue) error
	Commit(ctx context.Context, change func(tx Tx) (any, error)) (any, error)
	// Memo reads a durable memo of the tool task; ok is false when absent.
	Memo(ctx context.Context, name string) (value JsonValue, ok bool, err error)
	// MemoCandidate stores candidate unless a memo already exists and returns the durable winner.
	MemoCandidate(ctx context.Context, name string, candidate JsonValue) (JsonValue, error)
	// CreateTaskErased creates a task in the calling conversation; options.ConversationId is ignored.
	CreateTaskErased(ctx context.Context, task AnyTask, input JsonValue, options TaskOptions) (TaskId, error)
	// GetTask returns nil when absent.
	GetTask(ctx context.Context, id TaskId) (*TaskRecord[JsonValue, JsonValue, JsonValue], error)
	WaitForTask(ctx context.Context, id TaskId) (SettledTask[JsonValue], error)
	// Conversation returns an invocation-bound handle of an existing conversation, such as one this tool created in
	// Commit; nil when absent.
	Conversation(ctx context.Context, id ConversationId) (ConversationHandle, error)
}

// ToolReplay is whether an interrupted execution may rerun on recovery.
type ToolReplay string

const (
	ReplaySafe   ToolReplay = "safe"
	ReplayUnsafe ToolReplay = "unsafe"
)

// ToolOutputRetain is which end of oversized output a tool keeps.
type ToolOutputRetain string

const (
	RetainHead ToolOutputRetain = "head"
	RetainTail ToolOutputRetain = "tail"
)

// ToolOutputLimits bounds the retained output of a tool; nil fields use the defaults.
type ToolOutputLimits struct {
	MaxBytes *int
	MaxLines *int
	Retain   ToolOutputRetain
}

// ToolRegistration is an executable tool registered in a registry. Only the pi-ai Tool fields enter the transcript.
// Arguments are validated against Parameters before Execute; DefineTool types both.
type ToolRegistration struct {
	ai.ToolSchema
	// Replay defaults to unsafe.
	Replay ToolReplay
	// ExecutionMode defaults to the settings' toolExecution. One sequential call makes its whole round sequential.
	ExecutionMode ToolExecutionMode
	// PrepareArguments repairs arguments models commonly get wrong before validation, such as a JSON string where an
	// array belongs. It must be pure and must not mutate args: it runs again when a call is retried before its intent
	// is recorded. Its result is still validated against Parameters. Nil when absent.
	PrepareArguments func(args any) (any, error)
	OutputLimits     *ToolOutputLimits
	Execute          func(ctx context.Context, args any, api ToolExecutionApi) (ToolExecutionResult, error)
}

// PromptInput is the input to system prompt section rendering for one request preparation.
type PromptInput struct {
	ConversationId ConversationId
	// Agent is the request's resolution; Agent.Tools are the tools offered in this request.
	Agent Agent
	// Env is built by HarnessOptions.env for this preparation; nil without an environment.
	Env env.ExecutionEnv
	// Shown holds the sections already in effect after replaying the active transcript.
	Shown map[string]string
	// Read holds committed document reads.
	Read DocumentReader
}

// PromptSection is one system prompt section; the agent's sections render in order before each request.
type PromptSection struct {
	Key string
	// Render returns nil to omit the section.
	Render func(ctx context.Context, input PromptInput) (*string, error)
	// Tag defaults to true: wrap the text as `<key>\n...\n</key>`.
	Tag *bool
}

// HookRegistration is built by Hook; it matches tasks by name.
type HookRegistration struct {
	Task     string
	Handlers any
}

// Wrap is built by WrapTool (Tool and WrapTool set) and WrapSection (Section and WrapSection set); it targets a tool
// name or a section key. Wrappers are pure.
type Wrap struct {
	Tool        string
	WrapTool    func(tool *ToolRegistration) *ToolRegistration
	Section     string
	WrapSection func(section *PromptSection) *PromptSection
}

// Extension is a named bundle of code, installed in a registry and selected by conversations by name.
type Extension struct {
	Name     string
	Tools    []*ToolRegistration
	Sections []*PromptSection
	Hooks    []HookRegistration
	// Wraps apply where this extension is selected, in order.
	Wraps []Wrap
	// Tasks are resolved by name for every task, whichever conversations select this extension.
	Tasks []AnyTask
}

// RegistryTool is an installed tool with its extension.
type RegistryTool struct {
	Extension *Extension
	Tool      *ToolRegistration
}

// RegistrySection is an installed prompt section with its extension.
type RegistrySection struct {
	Extension *Extension
	Section   *PromptSection
}

// RegistrySnapshot is an immutable view of one published registry state.
type RegistrySnapshot interface {
	Installed() []*Extension
	// Extension returns nil when absent.
	Extension(name string) *Extension
	// Tools returns every installed tool with its extension, in install order. Names may repeat across extensions.
	Tools() []RegistryTool
	Sections() []RegistrySection
	// Tasks returns the built-in and installed task definitions.
	Tasks() []AnyTask
	// Task returns nil when absent.
	Task(name string) AnyTask
}

// Agent is a conversation's agent resolved against a registry snapshot and the settings.
type Agent struct {
	Model         *ModelRef
	ThinkingLevel ai.ModelThinkingLevel
	Extensions    []*Extension
	// Tools are the tools a request offers, in order.
	Tools []*ToolRegistration
	// Sections are the extension sections, then instructions when set.
	Sections     []*PromptSection
	Instructions *string
	Cwd          *string
}

// ConversationStreamOptions holds curated pi-ai request options; absent fields use pi-ai defaults.
type ConversationStreamOptions struct {
	Transport ai.Transport `json:"transport,omitempty"`
	TimeoutMs *int         `json:"timeoutMs,omitempty"`
	// MaxRetries is provider/SDK retries inside one request attempt.
	MaxRetries      *int               `json:"maxRetries,omitempty"`
	MaxRetryDelayMs *int               `json:"maxRetryDelayMs,omitempty"`
	Headers         map[string]string  `json:"headers,omitempty"`
	Metadata        JsonObject         `json:"metadata,omitempty"`
	CacheRetention  ai.CacheRetention  `json:"cacheRetention,omitempty"`
	Deferred        *ai.DeferredOption `json:"deferred,omitempty"`
}

// ConversationRetryPolicy holds durable generation attempt retries; the JSON shape of pi-ai RetryPolicy.
type ConversationRetryPolicy struct {
	Enabled         bool `json:"enabled"`
	MaxRetries      int  `json:"maxRetries"`
	BaseDelayMs     int  `json:"baseDelayMs"`
	MaxAgentDelayMs *int `json:"maxAgentDelayMs,omitempty"`
}

// CompactionPolicy holds the automatic compaction thresholds (spec §8.7); manual compaction ignores Enabled.
type CompactionPolicy struct {
	// Enabled controls threshold and overflow compaction.
	Enabled bool `json:"enabled"`
	// ReserveTokens is room kept free for the answer: generation blocks to compact above
	// contextWindow - reserveTokens.
	ReserveTokens int `json:"reserveTokens"`
	// KeepRecentTokens is the approximate size of the recent context a summary keeps verbatim.
	KeepRecentTokens int `json:"keepRecentTokens"`
	// BackgroundTokens starts background compaction this far below the blocking threshold; 0 disables it.
	BackgroundTokens int `json:"backgroundTokens"`
}

// CompactionReason is why a compaction runs: Compact(), a threshold in generation preparation, or a context overflow.
type CompactionReason string

const (
	CompactionManual    CompactionReason = "manual"
	CompactionThreshold CompactionReason = "threshold"
	CompactionOverflow  CompactionReason = "overflow"
)

// Settings holds the resolved settings: every field over its built-in default, object fields merged.
type Settings struct {
	// Extensions is nil for every installed extension, in install order.
	Extensions    []*Extension
	Stream        ConversationStreamOptions
	Retry         ConversationRetryPolicy
	Compaction    CompactionPolicy
	ToolExecution ToolExecutionMode
	SteeringMode  QueueMode
	FollowUpMode  QueueMode
}

// ContextView is the raw active transcript and the derived model context.
type ContextView struct {
	// Head is the newest applicable head marker; nil when none.
	Head *EntryRecord
	// Entries are the raw active entries: the head marker followed by non-head entries from its head through the
	// tail.
	Entries []EntryRecord
	// Contributions holds, per entry of Entries, its model messages after edits and excluded stop reasons, before
	// tool result ordering.
	Contributions [][]ai.Message
	// Messages is the model context for the next provider request.
	Messages []ai.Message
}
