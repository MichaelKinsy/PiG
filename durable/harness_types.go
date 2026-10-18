package durable

import (
	"context"
	"encoding/json"
	"slices"

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

// InputEntry is the entry an input is placed as, instead of pi.user: an application kind, and data the transcript shows,
// such as its author. Its model message is still the input's user message (types.ts InputEntry).
type InputEntry struct {
	// Kind is a non-empty string that does not start with "pi.".
	Kind string `json:"kind"`
	// Data is the JSON payload of the placed entry; nil when absent.
	Data JsonValue `json:"data,omitempty"`
}

// SubmissionDraft is a host submission: user input (type input, with Content, an optional WhenBusy and an optional
// InputEntry) that may start a run, or a passive entry write (type write, with Entry).
type SubmissionDraft struct {
	// RequestId is nil when absent.
	RequestId *string
	Type      SubmissionType
	Content   UserInput
	WhenBusy  WhenBusy
	// InputEntry places an input as that entry; nil places it as pi.user without data. Only input submissions carry it
	// (types.ts SubmissionDraft: an input's `entry` is an InputEntry, a write's is an EntryDraft).
	InputEntry *InputEntry
	Entry      *EntryDraft
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
	// Output is for the model; nil when omitted: the retained output() text becomes the output.
	Output []ai.ToolResultMessageContent
	// StructuredOutput is for programs that call the tool (executeTool() callers, code mode scripts), present only when
	// HasStructuredOutput is true. A tool that declares a structured output schema must return one unless the result is
	// an error, and it is validated against the schema; a tool without a schema must not return one, and programs get
	// the output itself.
	StructuredOutput    JsonValue
	HasStructuredOutput bool
	IsError             *bool
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
	// Models is HarnessOptions.models: the catalog, credentials, and request transforms generation uses.
	Models() Models
	// Env is built by HarnessOptions.env for this call; nil without an environment.
	Env() env.ExecutionEnv
	// Now is the Harness clock (HarnessOptions.Now), for times a rerun after a restart compares against.
	Now() float64
	// Output appends running output, a string or []byte; it becomes the result content when the result omits
	// Content. The optional skipped marks a chunk that follows output an environment omitted, as reported by an
	// environment given OutputWindow (ShellOutputInfo.Skipped). The omitted output is more than the tail window by at
	// least one byte or line, and only tail retention accepts it. Only the first skipped counts.
	Output(chunk any, skipped ...env.ShellOutputSkip)
	// OutputWindow is the tail this call's output keeps and the pace of its progress commits, for
	// ShellExecOptions.Window; nil when the tool keeps the head of its output, which cannot accept skips. A wrapper
	// that replaces Output and transforms text must also replace this with nil, so skipped text cannot bypass its
	// transform.
	OutputWindow() *env.ShellOutputWindow
	// RetainedOutput is the output retained so far, as the model will see it when the result omits Output, and whether
	// earlier output was dropped; for a tool that also returns that text in its StructuredOutput.
	RetainedOutput() (text string, truncated bool)
	// Diagnostic records a model-visible remark about this call.
	Diagnostic(diagnostic ToolDiagnostic)
	// Details replaces running details; the last value becomes the result details when the result omits Details.
	Details(ctx context.Context, value JsonValue) error
	Commit(ctx context.Context, change func(tx Tx) (any, error)) (any, error)
	// Memo reads a durable memo of the tool task; ok is false when absent.
	Memo(ctx context.Context, name string) (value JsonValue, ok bool, err error)
	// MemoCandidate stores candidate unless a memo already exists and returns the durable winner.
	MemoCandidate(ctx context.Context, name string, candidate JsonValue) (JsonValue, error)
	// CreateTaskErased creates a task in one commit in the calling conversation; options.ConversationId is ignored. A
	// child task of a tool that is not replay-safe defaults to AbandonOnRestart: the tool never resumes after a restart,
	// so nothing awaits the child then (harness/types.ts:232-234).
	CreateTaskErased(ctx context.Context, task AnyTask, input JsonValue, options TaskOptions) (TaskId, error)
	// GetTask returns nil when absent.
	GetTask(ctx context.Context, id TaskId) (*TaskRecord[JsonValue, JsonValue, JsonValue], error)
	WaitForTask(ctx context.Context, id TaskId) (SettledTask[JsonValue], error)
	// Conversation returns an invocation-bound handle of an existing conversation, such as one this tool created in
	// Commit; nil when absent.
	Conversation(ctx context.Context, id ConversationId) (ConversationHandle, error)
	// ExecuteTool calls another tool as a nested call: its own pi.tool task owned by this call, which runs validation, the
	// ToolTask hooks (whose ToolHookCall carries the parent), and the tool, and whose result comes back here, not in
	// the transcript. A nested call that ends in an error result still returns it; the error return is for a call that
	// could not be made or waited for. Calls made without a key are keyed by their order, so a rerun that calls in the
	// same order reattaches to the calls it made. Cancelling ctx stops only the wait.
	ExecuteTool(ctx context.Context, name string, args map[string]any, options ...NestedCallOptions) (NestedToolExecutionResult, error)
}

// NestedCallOptions are the options of ExecuteTool (types.ts ToolExecutionApi.executeTool).
type NestedCallOptions struct {
	// Key names the call among this call's nested calls, so a rerun finds it. It is a path segment of the nested call's
	// ID: non-empty, without "/", not "__proto__", and not a positive integer, which default keys use. Nil: the call's
	// order among this call's nested calls.
	Key *string
	// Progress false commits no running output, details, or diagnostics to the call's slot; the result still gets them.
	Progress *bool
	// ArgumentsJSON is args as JSON text in the caller's member order, such as the text a script's object came from. A Go
	// map has no order, and Pi keeps the order of the arguments object: the stored call, its slot, validation, hooks and
	// the execute intent write args' members in this order. Members it lacks follow in sorted order. Nil: sorted order.
	ArgumentsJSON json.RawMessage
}

// NestedToolExecutionResult is the result of a nested call, as ExecuteTool returns it. Programs read StructuredOutput;
// the output for the model is not kept, and Control does not apply.
type NestedToolExecutionResult struct {
	// TaskId is the nested call's tool task.
	TaskId TaskId
	// StructuredOutput is the tool's value for programs, present when HasStructuredOutput. A tool with a schema: its
	// validated structured output, also on an error result that has one; absent otherwise, and an error's text is then in an
	// error diagnostic. A tool without: its bounded output, success or error, as a string for one text item, an ImageContent
	// for one image, "" for none, and the content list otherwise. Absent on an error result the Harness wrote itself
	// (unavailable, invalid, blocked, interrupted, aborted); Diagnostics say what went wrong.
	StructuredOutput    JsonValue
	HasStructuredOutput bool
	IsError             bool
	Details             JsonValue
	HasDetails          bool
	Diagnostics         []ToolDiagnostic
	Usage               *ai.Usage
	// DurationMs is the execution time of the attempt that produced the result; nil when it never executed.
	DurationMs *int64
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
	// StructuredOutputSchema is the JSON schema of StructuredOutput, which programs that call the tool receive. Nil: they
	// receive the result's bounded output, success or error.
	StructuredOutputSchema map[string]any
	// Callers is who may call the tool: the model, other tools through executeTool(), or both (nil, the default). A
	// code mode tool is {CallerModel}, so scripts cannot start scripts; tools behind code mode, such as MCP tools, can
	// be {CallerTools}, so the model is never offered them.
	Callers []ToolCaller
	// Deferred tools are not offered to the model until loaded: the model gets the tool only when modelTools names it, in
	// its exact list or its add, which a tool such as tool_search writes with the addTools control. Other tools can call it
	// any time.
	Deferred bool
	Execute  func(ctx context.Context, args any, api ToolExecutionApi) (ToolExecutionResult, error)
}

// ToolCaller is a kind of caller a tool admits (types.ts ToolRegistration.callers).
type ToolCaller string

// The kinds of tool callers.
const (
	CallerModel ToolCaller = "model"
	CallerTools ToolCaller = "tools"
)

// CallableBy reports whether the tool admits the caller kind: a tool without callers admits both.
func (tool *ToolRegistration) CallableBy(caller ToolCaller) bool {
	return tool.Callers == nil || slices.Contains(tool.Callers, caller)
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

// Wrap is upstream's `{ tool; wrap(tool) } | { section; wrap(section) }` (harness/types.ts:263-265): a sealed union of ToolWrap, built by WrapTool,
// and SectionWrap, built by WrapSection. A wrap targets a tool name or a section key. Wrappers are pure.
type Wrap interface{ isWrap() }

// ToolWrap is the `{ tool: string; wrap(tool: Tool): Tool }` member of Wrap.
type ToolWrap struct {
	Tool string
	Wrap func(tool *ToolRegistration) *ToolRegistration
}

// SectionWrap is the `{ section: string; wrap(section: PromptSection<Tool>): PromptSection<Tool> }` member of Wrap.
type SectionWrap struct {
	Section string
	Wrap    func(section *PromptSection) *PromptSection
}

func (ToolWrap) isWrap()    {}
func (SectionWrap) isWrap() {}

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
	// Tools are the tools a request offers, in order: enabled, callable by the model, and selected by modelTools.
	Tools []*ToolRegistration
	// Callable are the tools nested calls (executeTool()) resolve among, in order: enabled and callable by tools.
	Callable []*ToolRegistration
	// Deferred are the enabled deferred tools the model may call and is not offered yet, in registry order: what tool_search loads.
	Deferred []*ToolRegistration
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
	Metadata        map[string]any     `json:"metadata,omitempty"`
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
	// ModelOverrides is the resolved CompactionSettings.modelOverrides, keyed by exact "provider/modelId"; nil without
	// any. PolicyFor applies it.
	//portlint:allow emptydrop a nil map is Pi's absent modelOverrides (Settings spreads only the keys a host set); an explicit {} is kept
	ModelOverrides map[string]CompactionPolicyOverride `json:"modelOverrides,omitzero"`
}

// CompactionPolicyOverride is Partial<CompactionPolicy>: one model's entry of modelOverrides; nil fields keep the
// Harness-wide value.
type CompactionPolicyOverride struct {
	Enabled          *bool `json:"enabled,omitempty"`
	ReserveTokens    *int  `json:"reserveTokens,omitempty"`
	KeepRecentTokens *int  `json:"keepRecentTokens,omitempty"`
	BackgroundTokens *int  `json:"backgroundTokens,omitempty"`
}

// PolicyFor is compaction.ts compactionPolicy: the model's modelOverrides entry over the Harness-wide policy, field by
// field. The result carries no overrides.
func (policy CompactionPolicy) PolicyFor(model ModelRef) CompactionPolicy {
	resolved := CompactionPolicy{Enabled: policy.Enabled, ReserveTokens: policy.ReserveTokens, KeepRecentTokens: policy.KeepRecentTokens, BackgroundTokens: policy.BackgroundTokens}
	override, ok := policy.ModelOverrides[model.Provider+"/"+model.ModelId]
	if !ok {
		return resolved
	}
	if override.Enabled != nil {
		resolved.Enabled = *override.Enabled
	}
	if override.ReserveTokens != nil {
		resolved.ReserveTokens = *override.ReserveTokens
	}
	if override.KeepRecentTokens != nil {
		resolved.KeepRecentTokens = *override.KeepRecentTokens
	}
	if override.BackgroundTokens != nil {
		resolved.BackgroundTokens = *override.BackgroundTokens
	}
	return resolved
}

// ProgressPolicy is how often running progress is committed. Each progress commit is a storage write; a host whose
// storage is remote can commit less often, so live answers and tool output appear in larger steps.
type ProgressPolicy struct {
	// PartialIntervalMs is the minimum pause between commits of the answer being generated.
	PartialIntervalMs float64 `json:"partialIntervalMs"`
	// OutputIntervalMs is the minimum pause between commits of running tool output; large commits also pause in
	// proportion to their size.
	OutputIntervalMs float64 `json:"outputIntervalMs"`
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
	Progress      ProgressPolicy
	ToolExecution ToolExecutionMode
	SteeringMode  QueueMode
	FollowUpMode  QueueMode
	// ContextRetentionMs is how long an idle conversation keeps its last context read in memory, so its next run reads
	// only newer entries. Busy conversations always keep it; 0 drops it once the conversation is idle.
	ContextRetentionMs float64
	// DocumentRetentionMs is how long a loaded document that no watch or document state observes stays in memory after
	// its last read or write; the next access loads it from Storage. Observed documents always stay. Default ten
	// minutes; 0 drops one right after use; +Inf keeps them.
	DocumentRetentionMs float64
	// AfterFailedRun is what a run that fails (model_error, no_model) does with the inputs queued behind it.
	AfterFailedRun AfterFailedRun
	// RestartLimit is how many times in a row a task may be cut off by a crash, eviction, or reset (a close does not
	// count) without changing its state before it is parked as blocked (restart_limit); Harness.RetryTask runs it
	// again. +Inf never parks.
	RestartLimit float64
}

// AfterFailedRun is what a failed run does with the inputs queued behind it (HarnessSettings.afterFailedRun).
type AfterFailedRun string

// The failed-run policies: keep leaves the inputs queued until the next input or queued write, continue starts the next
// run with them, as an answer would, and withdraw settles them unanswered with reason run_failed.
const (
	AfterFailedRunKeep     AfterFailedRun = "keep"
	AfterFailedRunContinue AfterFailedRun = "continue"
	AfterFailedRunWithdraw AfterFailedRun = "withdraw"
)

// DefaultRestartLimit is the default of HarnessSettings.RestartLimit.
const DefaultRestartLimit = 5

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
