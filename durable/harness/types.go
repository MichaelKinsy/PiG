// Ports packages/durable/src/harness/types.ts: the declarations that src/types.ts does not need. The rest live in the root package (durable/harness_types.go).

package harness

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// HookApi is what a hook may use: committed reads and the asking task's memos, which hooks and the task share.
type HookApi interface {
	durable.DocumentReader
	TaskId() durable.TaskId
	ConversationId() durable.ConversationId
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

// BeforeToolResult is a beforeTool decision: a non-nil Block blocks the call, otherwise non-nil Arguments replace its arguments.
type BeforeToolResult struct {
	Arguments durable.JsonObject
	Block     *string
}

// ToolHooks are the hooks of the built-in tool task. A nil member does not run. Register them as *ToolHooks.
type ToolHooks struct {
	// BeforeTool runs before intent; the first block wins, otherwise arguments replace the call's arguments. An error blocks.
	BeforeTool func(ctx context.Context, call ai.ToolCall, api HookApi) (*BeforeToolResult, error)
	// AfterTool runs after execution, before the result entry; a non-nil result replaces the result.
	AfterTool func(ctx context.Context, call ai.ToolCall, result durable.ToolExecutionResult, api HookApi) (*durable.ToolExecutionResult, error)
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
	// Models is the pi-ai model access used by generation and compaction: a Model Runtime or a pi-ai collection.
	Models   durable.Models
	Registry RegistryReader
	// Settings returns the current settings at every resolution; nil uses the defaults. Upstream reads a settings object whose fields may be getters.
	Settings func() *HarnessSettings
	// Env builds a conversation's environment at each use. It never runs on the Session line. Nil means no environment.
	Env func(ctx context.Context, target EnvTarget) (env.ExecutionEnv, error)
	// ConversationCreated runs in every commit that creates or forks a conversation, raw tx.CreateConversation included, after the built-in pi.* documents and before the conveniences apply the agent change and run init. A fork already has its copies. Table reads fail with ReadAfterWrite; an error fails the creating commit.
	ConversationCreated func(tx durable.Tx, conversation durable.ConversationRecord) error
	// Now is the Harness clock in milliseconds; nil uses the wall clock.
	Now func() float64
	// OnReport receives extension failures that do not fail the calling operation. It must not panic.
	OnReport func(err error)
}

// TaskInspectionKind is the kind of a TaskInspectionState.
type TaskInspectionKind string

const (
	TaskInspectionRunning    TaskInspectionKind = "running"
	TaskInspectionReady      TaskInspectionKind = "ready"
	TaskInspectionWaiting    TaskInspectionKind = "waiting"
	TaskInspectionCompleting TaskInspectionKind = "completing"
	TaskInspectionBlocked    TaskInspectionKind = "blocked"
)

// TaskInspectionState is what the scheduler would do with a live task: running (an invocation is active); ready (the next scheduling pass reserves it; Migrates when its definition is newer and has a migration); waiting (on these live tasks: the live part of its on, or, when abort-marked, its live ordinary owned work); completing (outcome held until its ordinary owned work drains); blocked (no registered definition can take it, with Reason missing_task, task_too_old, or migration_failed and an optional Error).
type TaskInspectionState struct {
	Kind     TaskInspectionKind
	Migrates bool
	On       []durable.TaskId
	Reason   string
	Error    error
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
	Context(ctx context.Context) (durable.ContextView, error)
	// Entries returns the newest-first fork-aware history of this conversation; query.ConversationId is ignored.
	Entries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error)
	Fork(ctx context.Context, at durable.EntryId, options ConversationCreateOptions) (Conversation, error)
	// Abort withdraws queued inputs (queued writes stay), marks every live non-background task of the ordinary ownership scope, signals them, and returns once the scope is idle. Background subtrees survive unless options.Background is set.
	Abort(ctx context.Context, options *durable.ConversationAbortOptions) error
	// WaitForIdle returns when the ordinary ownership scope has no live non-background task.
	WaitForIdle(ctx context.Context) error
	// ViewState returns the structural view (spec §9.3) as a disposable read-only Chord state.
	ViewState(ctx context.Context) (durable.AttachedReplicatedState[ConversationView], error)
	// Watch returns the structural view as a serialized exact-frame watch with bounded pending frames.
	Watch(ctx context.Context) (ConversationWatch, error)
}

// Harness is a durable agent harness over one Session.
type Harness interface {
	durable.Session
	// Resume enables task scheduling. It is idempotent and panics after close. Calls that ask for progress enable it too: Conversation.Submit, Compact, Abort, WaitForIdle, Submission.Wait, WaitForTask, and Harness.WaitForIdle. Read-only viewers never do.
	Resume()
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
	// AbortTask commits the abort mark, signals and joins an active run invocation, and schedules the abort invocation. A task whose definition cannot take it settles as orphaned instead. It returns marked or terminal.
	AbortTask(ctx context.Context, id durable.TaskId) (string, error)
	// WaitForTask returns the terminal receipt; cancelling ctx cancels only this wait.
	WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error)
	// WaitForIdle returns when the ordinary ownership scope of every ownerless conversation has no live non-background task.
	WaitForIdle(ctx context.Context) error
	// Usage is the Session total: every conversation's pi.usage summed.
	Usage(ctx context.Context) (UsageState, error)
	// TaskGraph returns every live task with its owner edge, status, and owned conversations (spec §9.5), as a disposable Chord state.
	TaskGraph(ctx context.Context) (durable.AttachedReplicatedState[TaskGraph], error)
	// WatchTaskGraph returns the task graph as a serialized exact-frame watch with bounded pending frames.
	WatchTaskGraph(ctx context.Context) (TaskGraphWatch, error)
}
