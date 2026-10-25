// Ports packages/durable/src/harness/types.ts: the declarations that src/types.ts does not need. The rest live in the root package (durable/harness_types.go).

package harness

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// HookApi is what a hook may use: committed reads and the asking task's memos, which hooks and the task share.
type HookApi interface {
	durable.DocumentReader
	TaskId() durable.TaskId
	ConversationId() durable.ConversationId
	// Models is HarnessOptions.models.
	Models() durable.Models
	// Memo reads a memo; ok is false when absent.
	Memo(ctx context.Context, name string) (value durable.JsonValue, ok bool, err error)
	// MemoCandidate stores candidate unless a memo already exists and returns the durable winner.
	MemoCandidate(ctx context.Context, name string, candidate durable.JsonValue) (durable.JsonValue, error)
}

// GenerationRequest is the request a beforeRequest hook may replace.
type GenerationRequest struct {
	Messages []ai.Message
}

// YieldContinue is the onYield result that appends a user message and continues the run.
type YieldContinue struct {
	Continue durable.UserInput
}

// GenerationHooks are the hooks of the built-in generation task. A nil member does not run. Register them as *GenerationHooks.
type GenerationHooks struct {
	// BeforeRequest runs before every request attempt, including recovery; a non-nil result is used for that request only.
	BeforeRequest func(ctx context.Context, request GenerationRequest, api HookApi) (*GenerationRequest, error)
	// AfterResponse sees every terminal provider message, before classification.
	AfterResponse func(ctx context.Context, message ai.AssistantMessage, api HookApi) error
	// OnYield sees a final answer; the first non-nil result appends a user message and continues the run.
	OnYield func(ctx context.Context, answer ai.AssistantMessage, api HookApi) (*YieldContinue, error)
	// AfterTools runs after every tool of the round is terminal; results are the round's result entries in call order.
	AfterTools func(ctx context.Context, assistant durable.EntryId, results []durable.EntryId, api HookApi) error
}

// ToolParent is the calling task and call ID of a nested call.
type ToolParent struct {
	TaskId durable.TaskId
	CallId string
}

// ToolHookCall is a call as tool hooks see it; Parent is set for a nested call, one a running tool made through
// ExecuteTool (types.ts ToolHookCall).
type ToolHookCall struct {
	// Type is "toolCall": the discriminator of pi-ai's ToolCall, which Go carries as the type of the call.
	Type string
	ai.ToolCall
	Parent *ToolParent
}

// NewToolHookCall is the ToolHookCall for call, with parent set for a nested call.
func NewToolHookCall(call ai.ToolCall, parent *ToolParent) ToolHookCall {
	return ToolHookCall{Type: "toolCall", ToolCall: call, Parent: parent}
}

// BeforeToolResult is a beforeTool decision: a non-nil Block blocks the call, otherwise non-nil Arguments replace its arguments.
type BeforeToolResult struct {
	Arguments map[string]any
	Block     *string
}

// ToolHooks are the hooks of the built-in tool task. A nil member does not run. Register them as *ToolHooks.
type ToolHooks struct {
	// BeforeTool runs before intent; the first block wins, otherwise arguments replace the call's arguments. An error blocks.
	BeforeTool func(ctx context.Context, call ToolHookCall, api HookApi) (*BeforeToolResult, error)
	// AfterTool runs after execution, before the result entry; a non-nil result replaces the result.
	AfterTool func(ctx context.Context, call ToolHookCall, result durable.ToolExecutionResult, api HookApi) (*durable.ToolExecutionResult, error)
}

// CompactionRequest is what a beforeCompact hook sees: Entries are the active entries the summary replaces, the head marker first, and Messages their model context, the summarizer's source; FirstKept is the first entry kept verbatim.
type CompactionRequest struct {
	Reason       durable.CompactionReason
	Entries      []durable.EntryRecord
	Messages     []ai.Message
	FirstKept    durable.EntryId
	Instructions *string
}

// CompactionDecision is a beforeCompact decision: Decline, or a Summary to use instead of summarizing.
type CompactionDecision struct {
	Decline bool
	Summary *string
}

// CompactionHooks are the hooks of the built-in compaction task. A nil member does not run. Register them as *CompactionHooks.
type CompactionHooks struct {
	// BeforeCompact runs after range selection, before summarizing; the first decision wins.
	BeforeCompact func(ctx context.Context, compaction CompactionRequest, api HookApi) (*CompactionDecision, error)
}

// ConversationInit runs inside the creating commit, after the creation hook and the agent change. The conversation creation is already a table write, so table reads here fail with ReadAfterWrite; document access remains available.
type ConversationInit func(tx durable.Tx, conversationId durable.ConversationId) error

// ConversationCreateOptions are the options of creating or forking a conversation.
type ConversationCreateOptions struct {
	Ownership durable.ConversationOwnership
	// Agent is applied in the creating commit after the creation hook's copy, before Init; nil when absent.
	Agent *AgentChange
	Init  ConversationInit
}

// RootOptions are the options of Harness.Root.
type RootOptions struct {
	Agent *AgentChange
	Init  ConversationInit
}

// CompactionResult carries the EntryId of a blocking compaction's summary, or the SubmissionId of a conversation-owned compaction's summary write; both nil when nothing was compacted.
type CompactionResult struct {
	EntryId      *durable.EntryId      `json:"entryId,omitempty"`
	SubmissionId *durable.SubmissionId `json:"submissionId,omitempty"`
}

// EnvTarget is what HarnessOptions.Env builds an environment for.
type EnvTarget struct {
	ConversationId durable.ConversationId
	// Cwd is the conversation's agent cwd; nil when unset.
	Cwd  *string
	Read durable.DocumentReader
}

// HarnessOptions are the options of OpenHarness.
type HarnessOptions struct {
	// Telemetry is the host-owned adapter, HarnessOptions.telemetry, for content-free admission and phase telemetry
	// (DurableTelemetrySchema). Its Propagation() and Metrics() capabilities are optional (nil is absent); a nil
	// Telemetry is the omitted option, the no-op context.
	Telemetry telemetry.TelemetryContext
	// Models is the pi-ai model access used by generation, compaction, hooks and tools: a Model Runtime or a pi-ai
	// collection. ModelsFunc, when set, replaces it: it returns the current one and is called at each use, so a host can
	// replace its model configuration without reopening the Harness (HarnessOptions.models as a function, Pi 7ec1f648).
	Models     durable.Models
	ModelsFunc func() durable.Models
	// Identity is how model requests identify the app to providers, merged field by field over DefaultHarnessIdentity (attribution to PiG). It applies to every request through the Harness's models, including hooks and tools, and overrides the identity the models were created with; a request's own Identity overrides it. Pass an identity with AttributionNull to send no attribution headers (HarnessOptions.identity).
	Identity *ai.ClientIdentity
	// plainModels is Models with Identity applied, wrapped once by WithModelsView; Pi wraps a plain Models once and a function's result at each call.
	plainModels durable.Models
	Registry    RegistryReader
	// Settings returns the current settings at every resolution; nil uses the defaults. Upstream reads a settings object whose fields may be getters.
	// It is called from the Harness's own goroutines (the Session line, the scheduler, task runtimes, graph views), concurrently with the host and
	// with itself, and the returned value is read after the call returns. It must be safe for concurrent use and the value must not be written once
	// returned. Publish a change by returning a new value, for example from an atomic.Pointer.
	Settings func() *HarnessSettings
	// Env builds a conversation's environment at each use. It never runs on the Session line. Nil means no environment.
	Env func(ctx context.Context, target EnvTarget) (env.ExecutionEnv, error)
	// ConversationCreated runs in every commit that creates or forks a conversation, raw tx.CreateConversation included, after the built-in pi.* documents and before the conveniences apply the agent change and run init. A fork already has its copies. Table reads fail with ReadAfterWrite; an error fails the creating commit.
	ConversationCreated func(tx durable.Tx, conversation durable.ConversationRecord) error
	// Now is the Harness clock, epoch milliseconds; nil uses the wall clock. Sleeping tasks store deadlines on it, so
	// every process that opens the same storage must use the same clock.
	Now func() float64
	// SetTimer arms the timers of the Session, the scheduler, and its tasks: pacing progress commits, freeing idle
	// memory (background), and, without OnWake, resuming sleeping tasks; and the timers of TaskRuntime.SetTimer. The
	// Node.js environment, file watcher, and image worker keep their own timers. Nil uses Go timers. Tests can pass fake
	// timers; a host can keep timers off a process that should hibernate.
	SetTimer durable.SetTimer
	// OnWake wakes the process for sleeping tasks instead of a SetTimer timer. Without it, while scheduling runs, a timer
	// that keeps the process alive resumes sleeping tasks when they are due. With it, the Harness arms no timer for
	// them: it calls OnWake after open and whenever Harness.Wake() changes, and the host arms its own wakeup, such as a
	// Durable Object alarm, and calls Resume() when it fires. Called off the Session line, after the commit that changed
	// the value, so a host arms a recovery wakeup before calls that can start work. It may call Wake and Resume, and
	// must not block. A panic is reported, and the value is offered again at the next change.
	OnWake func(wake Wake)
	// OnReport receives failures that do not fail the calling operation, such as a panicking hook or listener, a task
	// that faulted, and the error that fails the Session, once. info says what happened: a host can tell a failed
	// Session (ReportSessionFailed, often a platform reset) from a faulted task (ReportTaskFaulted) or a parked one
	// (ReportTaskParked). A panic of its own is dropped.
	OnReport func(err error, info durable.ReportInfo)
	// ReadOnly opens for reading only, as a viewer of a session that another process drives, for example a synced copy on a
	// device that only shows it. Open recovers nothing: a running task stays as committed. Every commit with writes fails
	// with *durable.ReadOnlySession before it reaches Storage, and so does Resume (a panic, as it throws upstream) and every
	// call that enables scheduling; the Harness stays usable for reads and watches. Wake is always inactive, and OnWake is
	// never called. Reopen without it to write.
	ReadOnly bool
}

// Wake is when a Session next needs a running process (Harness.Wake). Active: some task can run now or is running, so a
// process that dies must be woken again soon, for example by a recovery alarm. At: the earliest sleep not yet due, when
// there is one. Neither: nothing needs a process until new work is submitted. Tasks blocked on a missing or unmigratable
// definition, completing tasks, and waits on live tasks do not count; the work they wait for does. Active is also set while
// a reconcile pass, such as the recovery of Open, has not landed yet: it may finalize held outcomes and mark cascades.
type Wake struct {
	Active bool
	// At is the earliest sleep not yet due; nil when there is none.
	At *float64
}

// TaskInspectionKind is the kind of a TaskInspectionState.
type TaskInspectionKind string

const (
	TaskInspectionRunning    TaskInspectionKind = "running"
	TaskInspectionReady      TaskInspectionKind = "ready"
	TaskInspectionWaiting    TaskInspectionKind = "waiting"
	TaskInspectionSleeping   TaskInspectionKind = "sleeping"
	TaskInspectionCompleting TaskInspectionKind = "completing"
	TaskInspectionBlocked    TaskInspectionKind = "blocked"
)

// TaskInspectionState is what the scheduler would do with a live task: running (an invocation is active); ready (the next scheduling pass reserves it; Migrates when its definition is newer and has a migration); waiting (on these live tasks: the live part of its on, or, when abort-marked, its live ordinary owned work); sleeping (until the Harness clock reaches Until); completing (outcome held until its ordinary owned work drains); blocked (no registered definition can take it, or the restart limit parked it, with Reason missing_task, task_too_old, migration_failed, or restart_limit and an optional Error).
type TaskInspectionState struct {
	Kind     TaskInspectionKind
	Migrates bool
	On       []durable.TaskId
	// Until is the time a sleeping task wakes at.
	Until  float64
	Reason string
	Error  error
}

// TaskInspection is a live task and what the scheduler would do with it under the current registry.
type TaskInspection struct {
	Record durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
	State  TaskInspectionState
}

// HarnessInspection is a point-in-time view of live work: unfinished tasks and submissions, read on the Session line. Scheduling is paused, running, or closing.
type HarnessInspection struct {
	Scheduling  string
	Tasks       []TaskInspection
	Submissions []durable.SubmissionRecord
}

// ConversationWatch is a serialized exact-frame watch of the structural view.
type ConversationWatch = durable.WatchHandle[ConversationView]

// Conversation is a stateless handle for one conversation, bound to the Harness that returned it. Compare handles by Id.
type Conversation interface {
	Id() durable.ConversationId
	// Agent resolves the agent with the current registry snapshot and settings.
	Agent(ctx context.Context) (durable.Agent, error)
	// Configure applies change in its own commit.
	Configure(ctx context.Context, change AgentChange) error
	// Submit durably admits user input or a passive entry write. A busy conversation, or one with queued items, queues it in pi.inbox; WhenBusy reject fails with ConversationBusy instead and writes nothing.
	Submit(ctx context.Context, submission durable.SubmissionDraft) (durable.Submission, error)
	// Reset admits a write of a pi.reset entry that starts a new context, carrying handoff as a user message when non-nil. It returns after admission; while busy, it is placed at the next boundary.
	Reset(ctx context.Context, handoff *string) error
	// Compact admits a manual compaction task and returns its ID (spec §8.7).
	Compact(ctx context.Context, instructions *string) (durable.TaskId, error)
	// Commit runs a Session commit whose tx.CreateTask defaults to this conversation.
	Commit(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error)
	// Context returns the committed raw active transcript and model context. With options.At, it returns the context as
	// of that visible entry: the same view Fork(at) would start with, without creating a conversation.
	Context(ctx context.Context, options *durable.ContextOptions) (durable.ContextView, error)
	// Entries returns the fork-aware history of this conversation, newest first unless query.Order is ScanAscending;
	// query.ConversationId is ignored.
	Entries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error)
	Fork(ctx context.Context, at durable.EntryId, options ConversationCreateOptions) (Conversation, error)
	// Abort withdraws queued inputs (queued writes stay), marks every live non-background task of the ordinary ownership scope, signals them, and returns once the scope is idle. Background subtrees survive unless options.Background is set.
	Abort(ctx context.Context, options *durable.ConversationAbortOptions) error
	// WaitForIdle returns when the ordinary ownership scope has no live non-background task.
	WaitForIdle(ctx context.Context) error
	// Watch returns the structural view (spec §9.3) as a serialized exact-frame watch with bounded pending frames.
	Watch(ctx context.Context) (ConversationWatch, error)
}

// Harness is a durable agent harness over one Session.
type Harness interface {
	durable.Session
	// Resume enables task scheduling. It is idempotent; it panics after close, and with *durable.ReadOnlySession on a read-only Harness. Calls that ask for progress enable it too: Conversation.Submit, Compact, Abort, WaitForIdle, Submission.Wait, WaitForTask, and Harness.WaitForIdle. Read-only viewers never do.
	Resume()
	// Wake is when this Session next needs a running process, from the scheduler's memory: Active while some task can run
	// now or is running, At for the earliest sleep. Answers also while scheduling is paused. A host that wakes the
	// process itself, such as a Durable Object alarm, arms from it and calls Resume when woken; see HarnessOptions.OnWake.
	Wake() Wake
	// Root returns the reserved root conversation, creating it with options.Agent and options.Init in one commit when absent.
	Root(ctx context.Context, options *RootOptions) (Conversation, error)
	// Conversation returns nil when absent.
	Conversation(ctx context.Context, id durable.ConversationId) (Conversation, error)
	CreateConversation(ctx context.Context, options ConversationCreateOptions) (Conversation, error)
	// GetTask returns nil when absent.
	GetTask(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error)
	// Inspect returns live tasks and unsettled submissions. It writes nothing and runs no task code.
	Inspect(ctx context.Context) (HarnessInspection, error)
	// Submission reacquires a submission, for example after reopen; nil when absent.
	Submission(ctx context.Context, id durable.SubmissionId) (durable.Submission, error)
	// AbortSubmission returns not_found for an unknown submission or one of another conversation than conversationId, when given.
	AbortSubmission(ctx context.Context, id durable.SubmissionId, conversationId *durable.ConversationId) (durable.SubmissionAbortResult, error)
	// RetryTask runs a task parked by settings.restartLimit again: it clears its restart count and enables scheduling. It
	// does nothing for a task that is not parked, and fails for an unknown one.
	RetryTask(ctx context.Context, id durable.TaskId) error
	// AbortTask commits the abort mark, signals and joins an active run invocation, and schedules the abort invocation. A task whose definition cannot take it settles as orphaned instead. It returns marked or terminal.
	AbortTask(ctx context.Context, id durable.TaskId) (string, error)
	// WaitForTask returns the terminal receipt; cancelling ctx cancels only this wait. A conversation-owned task nothing waits for commits in
	// an order set by goroutine scheduling against its conversation's generation, so its record can still be live when the answer settles
	// (D181); wait for it, or for WaitForIdle, before reading its state.
	WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error)
	// WaitForIdle returns when the ordinary ownership scope of every ownerless conversation has no live non-background task.
	WaitForIdle(ctx context.Context) error
	// Usage is the Session total: every conversation's pi.usage summed.
	Usage(ctx context.Context) (UsageState, error)
	// WatchTaskGraph returns every live task with its owner edge, status, and owned conversations (spec §9.5), as a
	// serialized exact-frame watch with bounded pending frames.
	WatchTaskGraph(ctx context.Context) (TaskGraphWatch, error)
}

// DefaultHarnessIdentity is the request identity of a Harness's model requests: the ai package's defaults with provider attribution to PiG.
var DefaultHarnessIdentity = ai.ClientIdentity{Attribution: new(ai.PiAttribution)}

// WithModelsView returns the options with a plain Models wrapped once with Identity applied over DefaultHarnessIdentity, so every CurrentModels returns the same view (harness.ts currentModels: "A plain Models is wrapped once; a function's result is wrapped at each call").
func (options HarnessOptions) WithModelsView() HarnessOptions {
	options.plainModels = nil
	if options.ModelsFunc == nil && options.Models != nil {
		options.plainModels = options.viewOf(options.Models)
	}
	return options
}

func (options HarnessOptions) viewOf(models durable.Models) durable.Models {
	merged := ai.MergeClientIdentity(&DefaultHarnessIdentity, options.Identity)
	return ai.WithClientIdentity(models, *merged)
}

// CurrentModels is HarnessOptions.models at this use, with Identity applied over DefaultHarnessIdentity: ModelsFunc's answer when set, else Models (harness.ts currentModels). Nil models stay nil.
func (options HarnessOptions) CurrentModels() durable.Models {
	if options.ModelsFunc == nil && options.plainModels != nil {
		return options.plainModels
	}
	models := options.Models
	if options.ModelsFunc != nil {
		models = options.ModelsFunc()
	}
	if models == nil {
		return nil
	}
	return options.viewOf(models)
}
