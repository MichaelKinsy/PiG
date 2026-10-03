package durable

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// Ports packages/durable/src/types.ts

// JsonValue is a strict JSON value: nil, bool, float64 or another JSON number, string, []any, or map[string]any.
type JsonValue = any

// JsonObject is the JSON object used as the root of every durable document.
type JsonObject = map[string]any

// Op is one Chord delta operation (packages/chord/src/delta).
type Op = delta.Op

// Draft is the revocable mutable working copy of a document of type T inside one commit. Reads and writes go through
// the Chord overlay handle, which records the exact operations; DecodeDoc[T](draft.Snapshot()) reads it as T. Using it
// after its commit settles panics with delta.ErrRevoked, as upstream throws.
type Draft[T any] = *delta.Object

// ConversationId identifies a conversation.
type ConversationId int64

// EntryId identifies a transcript entry.
type EntryId int64

// TaskId identifies a task. Upstream brands it with the task's result type; Go methods cannot take type parameters,
// so the result type is recovered at the generic helpers instead.
type TaskId int64

// SubmissionId identifies a submission.
type SubmissionId int64

// DocumentId identifies one document incarnation.
type DocumentId int64

// Id is any durable record ID; every kind shares the Session-global numeric namespace.
type Id interface {
	~int64
}

// Seq is the strictly increasing sequence assigned to one atomic storage commit; gaps are permitted.
type Seq int64

// ROOT_CONVERSATION_ID is the reserved ID of the root conversation.
//
//nolint:revive // upstream identifier
const ROOT_CONVERSATION_ID ConversationId = 1

// DocumentScope is the owner kind of a document.
type DocumentScope string

const (
	ScopeSession      DocumentScope = "session"
	ScopeConversation DocumentScope = "conversation"
	ScopeTask         DocumentScope = "task"
)

// DocumentHistory is how much history a conversation document keeps; empty for other scopes.
type DocumentHistory string

const (
	// HistoryLatest retains only the current state.
	HistoryLatest DocumentHistory = "latest"
	// HistoryRewindable retains the history needed for as-of reads.
	HistoryRewindable DocumentHistory = "rewindable"
)

// DocumentFork is how a fork initializes a conversation document; empty for other scopes.
type DocumentFork string

const (
	// ForkAsOf initializes from the source state at the fork cutoff; rewindable documents only.
	ForkAsOf DocumentFork = "asOf"
	// ForkCurrent initializes from the current source state.
	ForkCurrent DocumentFork = "current"
	// ForkInitial initializes from the definition's initial value.
	ForkInitial DocumentFork = "initial"
)

// DocumentSemantics is the ownership and lifetime of a document; only conversation documents declare history and
// fork behavior.
type DocumentSemantics struct {
	Scope   DocumentScope
	History DocumentHistory
	Fork    DocumentFork
}

// CheckpointInfo is the stored replay state supplied to a document's checkpoint predicate.
type CheckpointInfo struct {
	// DeltasSinceBase counts deltas already stored after the newest base, excluding the change being evaluated.
	DeltasSinceBase int
}

// CommonDocDefinition holds the definition fields shared by singleton documents and document families.
type CommonDocDefinition[T any] struct {
	// Kind is the stable persisted kind; part of the public protocol.
	Kind string
	// Version is the positive integer version of the stored value shape.
	Version int
	// Migrate converts a value stored by an older version; nil when absent.
	Migrate func(value JsonObject, fromVersion int) (T, error)
	// CheckpointWhen returns true to store this ordinary change as a complete base instead of a delta; nil when
	// absent.
	CheckpointWhen func(value T, ops []Op, info CheckpointInfo) bool
}

// DocDefinition is a singleton document definition.
type DocDefinition[T any] struct {
	CommonDocDefinition[T]
	DocumentSemantics
	Initial func() T
}

// DocFamilyDefinition is a keyed document family definition; Initial(seed) runs only when a member is absent.
type DocFamilyDefinition[T, I any] struct {
	CommonDocDefinition[T]
	DocumentSemantics
	Initial func(seed I) T
}

// DocToken is a typed singleton document token passed explicitly to typed access.
type DocToken[T any] struct {
	definition *AnyDocDefinition
}

// DocFamilyToken is a typed document family token passed explicitly to typed access.
type DocFamilyToken[T, I any] struct {
	definition *AnyDocDefinition
}

// The scope-refined token names of upstream. Go cannot refine a struct type by a field value, so each name aliases
// the general token and the Session checks the scope when it resolves an address.
type (
	SessionDocToken[T any]                         = DocToken[T]
	ConversationDocToken[T any]                    = DocToken[T]
	RewindableConversationDocToken[T any]          = DocToken[T]
	TaskDocToken[T any]                            = DocToken[T]
	SessionDocFamilyToken[T, I any]                = DocFamilyToken[T, I]
	ConversationDocFamilyToken[T, I any]           = DocFamilyToken[T, I]
	RewindableConversationDocFamilyToken[T, I any] = DocFamilyToken[T, I]
	TaskDocFamilyToken[T, I any]                   = DocFamilyToken[T, I]
)

// RunningTask is a live task record reserved by one invocation; State.Status is TaskRunning.
type RunningTask[I, S, R any] = TaskRecord[I, S, R]

// NextTaskState is the next state a task commits for itself: a replacement checkpoint (running), a wait, or its
// outcome (terminal). A returned terminal state is stored as completing while ordinary owned work below the task is
// live (spec §5.5).
type NextTaskState[S, R any] = TaskState[S, R]

// PhaseHandler runs one checkpoint phase. It must commit a changed checkpoint or a terminal outcome through
// runtime.Commit; returning without durable progress faults the task.
type PhaseHandler[I, S, R any, H any] = func(ctx context.Context, task RunningTask[I, S, R], runtime TaskRuntime[I, S, R, H]) error

// HookRunner dispatches one hook of a task to every matching registered handler, in registry order of the phase
// snapshot.
type HookRunner[H any] interface {
	// Each calls invoke with each matching handler set. An ordinary error from invoke is reported and the next handler
	// runs; once the invocation is signalled, the error propagates. Composition happens inside invoke. Go has no
	// keyed member access, so invoke receives the handler set and selects the member named name; handler sets that
	// leave name unset are skipped.
	Each(name string, invoke func(handlers H) error) error
}

// Models is the pi-ai Models access of a task runtime and the Harness (types.ts:425, scheduler.ts:136): the methods generation and compaction call. The coding worker passes its Model Runtime, and a bare pi-ai collection (*ai.Models) also satisfies it. Each optional options argument is the pi-ai optional parameter.
type Models interface {
	// GetModel looks up a chat model by provider and model ID against the last-known lists; nil when absent.
	GetModel(provider, id string) *ai.Model
	// StreamSimple streams one assistant message; failures end the stream with an error message.
	StreamSimple(ctx context.Context, model *ai.Model, request ai.Context, options ...ai.StreamOptions) *ai.AssistantMessageEventStream
	// CompleteSimple returns the terminal message of StreamSimple.
	CompleteSimple(ctx context.Context, model *ai.Model, request ai.Context, options ...ai.StreamOptions) *ai.AssistantMessage
	// FetchDeferred fetches the result of a deferred request.
	FetchDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredFetchOptions) *ai.AssistantMessage
	// CancelDeferred cancels a deferred request.
	CancelDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ...ai.DeferredCancelOptions) error
}

// TaskRuntime holds the operations of one task invocation. Every operation fails after the invocation ends; watches
// acquired through it stop at invocation end.
type TaskRuntime[I, S, R any, H any] interface {
	DocumentObserver
	DocumentReader

	TaskId() TaskId
	ConversationId() ConversationId
	// Signal is cancelled when the run is signalled by abortTask(), the Harness closes, or the invocation ends.
	Signal() context.Context
	// Registry is the registry snapshot of the current phase; refreshed at every phase boundary.
	Registry() RegistrySnapshot
	// Agent resolves the task's conversation's agent at most once per phase, at first use, fixed for the phase.
	Agent(ctx context.Context) (Agent, error)
	// Settings returns HarnessOptions.settings, resolved at each access.
	Settings() Settings
	Models() Models
	// Env calls HarnessOptions.env for the task's conversation; nil without an environment.
	Env(ctx context.Context) (env.ExecutionEnv, error)
	// Hooks returns the handlers of this task's name from the extensions its conversation selects, in extension
	// order.
	Hooks() HookRunner[H]

	// Commit commits on the Session line after rereading the task. It fails when the task is terminal, the
	// invocation ended, the Harness is closing, or, in a run invocation, the task carries an abort mark. A returned
	// state replaces the task's state in the same commit; nil leaves it unchanged. tx.CreateTask defaults to the task's
	// conversation.
	Commit(ctx context.Context, change func(tx Tx, current RunningTask[I, S, R]) (*NextTaskState[S, R], error)) error
	// Memo reads a durable memo of this task; ok is false when absent.
	Memo(ctx context.Context, name string) (value JsonValue, ok bool, err error)
	// MemoCandidate stores candidate unless a memo already exists and returns the durable winner.
	MemoCandidate(ctx context.Context, name string, candidate JsonValue) (JsonValue, error)
	// GetTask returns the committed task record; nil when absent.
	GetTask(ctx context.Context, id TaskId) (*TaskRecord[JsonValue, JsonValue, JsonValue], error)
	// WaitForTask returns the task's terminal receipt; it fails when the invocation ends.
	WaitForTask(ctx context.Context, id TaskId) (SettledTask[JsonValue], error)
	// Outcomes returns the outcomes of terminal tasks, in order; it fails when one is missing or not terminal.
	Outcomes(ctx context.Context, ids []TaskId) ([]TaskOutcome[JsonValue], error)
	// Conversation returns an invocation-bound handle of an existing conversation; nil when absent. Its operations
	// and the submissions it returns fail after the invocation ends; admitted work stays durable.
	Conversation(ctx context.Context, id ConversationId) (ConversationHandle, error)
	// Entry returns a committed entry visible from the task's conversation; nil when absent.
	Entry(ctx context.Context, id EntryId) (*EntryRecord, error)
	// Context returns the committed raw active transcript and model context, cut off at the visible entry at when at
	// is not nil.
	Context(ctx context.Context, conversationId ConversationId, at *EntryId) (ContextView, error)
	// Now returns the Harness clock.
	Now() float64
	// Report forwards a non-fatal failure to HarnessOptions.onReport.
	Report(err error)
	// Sleep returns once the Harness clock reaches until; it fails when the invocation or ctx is cancelled.
	Sleep(ctx context.Context, until float64) error
}

// TaskDefinition is an executable durable state machine definition, registered in the registry by Name.
type TaskDefinition[I, S, R any, H any] struct {
	// Name is the registered task kind persisted in TaskRecord.Kind.
	Name string
	// Version is the definition version persisted with live input and checkpoints.
	Version int
	// Initial returns the first durable checkpoint for a newly created task.
	Initial func(input I) S
	// Phases is the exhaustive phase map, keyed by the checkpoint's JSON "phase" member.
	Phases map[string]PhaseHandler[I, S, R, H]
	// Abort runs in a fresh invocation after an abort mark and must commit a terminal outcome.
	Abort func(ctx context.Context, task RunningTask[I, S, R], runtime TaskRuntime[I, S, R, H]) error
	// Migrate converts a record stored by any older supported version; runs at reservation. Nil when absent.
	Migrate func(input JsonValue, checkpoint JsonValue, fromVersion int) (I, S, error)
	// Hooks is the zero value when the task declares no hooks.
	Hooks H
}

// Task is a typed executable task definition.
type Task[I, S, R any, H any] struct {
	Definition *TaskDefinition[I, S, R, H]
	erased     *AnyTaskDefinition
}

// TaskOwnershipKind is "conversation" or "task".
type TaskOwnershipKind string

const (
	TaskOwnedByConversation TaskOwnershipKind = "conversation"
	TaskOwnedByTask         TaskOwnershipKind = "task"
)

// TaskOwnership names who owns a task: its conversation (a top-level task) or another task of the same
// conversation (a child task).
type TaskOwnership struct {
	Kind TaskOwnershipKind `json:"kind"`
	// TaskId is set only for kind "task".
	TaskId TaskId `json:"taskId,omitempty"`
}

// JoinPolicy is how a waiting task treats the tasks it waits on (spec §5.5).
type JoinPolicy string

const (
	JoinFailFast   JoinPolicy = "failFast"
	JoinAllSettled JoinPolicy = "allSettled"
)

// TaskOptions holds the creation options for a durable task.
type TaskOptions struct {
	// Ownership is required: a task always names its owner (spec §5.5).
	Ownership TaskOwnership
	// ConversationId defaults to the owner task's conversation, or the transaction's bound conversation; required for
	// conversation-owned tasks created by Session commits that are not bound to a conversation.
	ConversationId *ConversationId
	// Background applies to conversation-owned tasks only: excluded from ordinary idle waits, conversation aborts, and
	// cascades.
	Background bool
}

// ConversationOwnershipKind is "ownerless" or "task".
type ConversationOwnershipKind string

const (
	ConversationOwnerless   ConversationOwnershipKind = "ownerless"
	ConversationOwnedByTask ConversationOwnershipKind = "task"
)

// ConversationOwnership is selected explicitly whenever a conversation is created.
type ConversationOwnership struct {
	Kind ConversationOwnershipKind `json:"kind"`
	// TaskId is set only for kind "task".
	TaskId TaskId `json:"taskId,omitempty"`
}

// ConversationParent is the fork source and inclusive parent entry through which history is inherited.
type ConversationParent struct {
	ConversationId ConversationId `json:"conversationId"`
	At             EntryId        `json:"at"`
}

// ConversationOwner is the creator edge used for attribution, subtree abort, and subtree idle waits.
type ConversationOwner struct {
	ConversationId ConversationId `json:"conversationId"`
	TaskId         TaskId         `json:"taskId"`
}

// ConversationRecord is the immutable identity, history ancestry, and task ownership of a transcript scope.
type ConversationRecord struct {
	Id     ConversationId      `json:"id"`
	Parent *ConversationParent `json:"parent,omitempty"`
	Owner  *ConversationOwner  `json:"owner,omitempty"`
}

// ContextEditAction is "omit" or "replace".
type ContextEditAction string

const (
	EditOmit    ContextEditAction = "omit"
	EditReplace ContextEditAction = "replace"
)

// ContextEdit is an immutable override of one visible entry's contribution to model context.
type ContextEdit struct {
	// Target is the entry whose model messages are omitted or replaced.
	Target EntryId           `json:"target"`
	Action ContextEditAction `json:"action"`
	// Messages are contributed instead of the target entry's model messages; replace only.
	Messages []ai.Message `json:"messages,omitempty"`
}

// EntryRecord is an immutable transcript event with separate model-facing and application-facing payloads.
type EntryRecord struct {
	Id             EntryId        `json:"id"`
	ConversationId ConversationId `json:"conversationId"`
	// Kind is the application-defined entry discriminator.
	Kind string `json:"kind"`
	// Model holds the messages contributed to model context; nil for display or bookkeeping entries.
	Model []ai.Message `json:"model,omitempty"`
	// Data is the JSON payload consumed by views, extensions, or bookkeeping logic; HasData reports presence.
	Data JsonValue `json:"data,omitempty"`
	// Head is the first entry in the active context selected by this entry.
	Head *EntryId `json:"head,omitempty"`
	// Edits are context-only overrides of earlier visible entries.
	Edits []ContextEdit `json:"edits,omitempty"`
	// ByTaskId is the task that appended this entry, when it was produced by durable work.
	ByTaskId *TaskId `json:"byTaskId,omitempty"`
}

// EntryDraft is entry content supplied before the Session assigns identity and task attribution.
type EntryDraft struct {
	Kind  string        `json:"kind"`
	Model []ai.Message  `json:"model,omitempty"`
	Data  JsonValue     `json:"data,omitempty"`
	Edits []ContextEdit `json:"edits,omitempty"`
	// Head starts active context at an entry; HeadSelf starts it at the newly assigned entry ID.
	Head     *EntryId `json:"-"`
	HeadSelf bool     `json:"-"`
}

// TypedEntry is an entry whose Data has type D; D is Never when the kind carries no data.
type TypedEntry[D any] struct {
	EntryRecord
	TypedData D
}

// TypedEntryDraft is entry content of a typed kind; the token supplies Kind.
type TypedEntryDraft[D any] struct {
	Model    []ai.Message
	Data     D
	Edits    []ContextEdit
	Head     *EntryId
	HeadSelf bool
}

// Never is the data type of entry kinds that carry no data.
type Never struct{}

// Entry is a typed entry kind with a narrowing guard.
type Entry[D any] struct {
	Kind string
}

// SubmissionType is "input" or "write".
type SubmissionType string

const (
	SubmissionTypeInput SubmissionType = "input"
	SubmissionTypeWrite SubmissionType = "write"
)

// SubmissionStatus is the lifecycle status of a submission.
type SubmissionStatus string

const (
	// SubmissionQueued is admitted but not yet represented in the transcript.
	SubmissionQueued SubmissionStatus = "queued"
	// SubmissionPlaced is input added to the transcript and owned by an active run.
	SubmissionPlaced SubmissionStatus = "placed"
	// SubmissionDone is successfully answered input or a successfully appended passive entry.
	SubmissionDone SubmissionStatus = "done"
	// SubmissionUnanswered is terminal input that can no longer receive an answer, or a passive write that could not
	// be placed.
	SubmissionUnanswered SubmissionStatus = "unanswered"
)

// SubmissionRecord is the durable lifecycle of one admitted user input or passive entry write.
//
// Upstream encodes each (type, status) pair as a union member; the Go struct carries every field and the Session
// keeps them consistent: Entry is set for placed and done records and optional for unanswered input; Answer only for
// done input; Reason and Detail only for unanswered records.
type SubmissionRecord struct {
	Id             SubmissionId   `json:"id"`
	ConversationId ConversationId `json:"conversationId"`
	// RequestId is the host-provided deduplication key, scoped to the conversation; nil when absent.
	RequestId *string          `json:"requestId,omitempty"`
	Type      SubmissionType   `json:"type"`
	Status    SubmissionStatus `json:"status"`
	Entry     *EntryId         `json:"entry,omitempty"`
	Answer    *EntryId         `json:"answer,omitempty"`
	Reason    *string          `json:"reason,omitempty"`
	Detail    JsonValue        `json:"detail,omitempty"`
}

// SubmissionSettlement is the terminal status staged for a submission; identity, type, and entry come from its
// current record. Status is done (with Answer) or unanswered (with Reason and optional Detail).
type SubmissionSettlement struct {
	Status SubmissionStatus `json:"status"`
	Answer EntryId          `json:"answer,omitempty"`
	Reason string           `json:"reason,omitempty"`
	Detail JsonValue        `json:"detail,omitempty"`
}

// SubmissionCreate holds the submission fields supplied before the Session assigns an ID.
type SubmissionCreate struct {
	ConversationId ConversationId   `json:"conversationId"`
	RequestId      *string          `json:"requestId,omitempty"`
	Type           SubmissionType   `json:"type"`
	Status         SubmissionStatus `json:"status"`
	Entry          *EntryId         `json:"entry,omitempty"`
	Answer         *EntryId         `json:"answer,omitempty"`
	Reason         *string          `json:"reason,omitempty"`
	Detail         JsonValue        `json:"detail,omitempty"`
}

// Record returns the submission record of a create with its assigned ID.
func (create SubmissionCreate) Record(id SubmissionId) SubmissionRecord {
	return SubmissionRecord{
		Id: id, ConversationId: create.ConversationId, RequestId: create.RequestId, Type: create.Type,
		Status: create.Status, Entry: create.Entry, Answer: create.Answer, Reason: create.Reason, Detail: create.Detail,
	}
}

// TaskOutcomeError is the JSON-safe error snapshot persisted instead of a runtime error.
type TaskOutcomeError struct {
	Message string `json:"message"`
	// Detail is optional structured diagnostic data for inspection or recovery.
	Detail JsonValue `json:"detail,omitempty"`
}

// TaskOutcomeStatus classifies a terminal task.
type TaskOutcomeStatus string

const (
	OutcomeCompleted TaskOutcomeStatus = "completed"
	// OutcomeFailed is an expected task or domain failure explicitly committed by its implementation.
	OutcomeFailed TaskOutcomeStatus = "failed"
	// OutcomeAborted is explicit cancellation handled by the task's abort protocol.
	OutcomeAborted TaskOutcomeStatus = "aborted"
	// OutcomeOrphaned is a task that cannot resume because its definition or migration is unavailable.
	OutcomeOrphaned TaskOutcomeStatus = "orphaned"
	// OutcomeFaulted is a runtime-detected contract failure, such as an uncaught error or no durable progress.
	OutcomeFaulted TaskOutcomeStatus = "faulted"
)

// TaskOutcome is the durable reason and optional result recorded when a task becomes terminal. Result is required for
// completed and optional for failed and aborted; Error is required for failed and faulted; Reason is optional for
// aborted and required for orphaned.
type TaskOutcome[R any] struct {
	Status TaskOutcomeStatus `json:"status"`
	Result *R                `json:"result,omitempty"`
	Error  *TaskOutcomeError `json:"error,omitempty"`
	Reason *string           `json:"reason,omitempty"`
}

// TaskStatus is the execution status of a task.
type TaskStatus string

const (
	// TaskPending is eligible for scheduling.
	TaskPending TaskStatus = "pending"
	// TaskRunning is reserved by one in-memory task invocation.
	TaskRunning TaskStatus = "running"
	// TaskWaiting is parked without an invocation until every task in On is terminal; then resumes at Checkpoint.
	TaskWaiting TaskStatus = "waiting"
	// TaskCompleting has its outcome decided; it becomes terminal once no ordinary owned work below is live and runs
	// no more code.
	TaskCompleting TaskStatus = "completing"
	// TaskTerminal is the permanently settled durable result receipt.
	TaskTerminal TaskStatus = "terminal"
)

// TaskState is the complete durable execution state of a task. Live statuses (pending, running, waiting) carry
// Checkpoint; waiting also carries On and Policy; completing and terminal carry Outcome.
type TaskState[S, R any] struct {
	Status     TaskStatus      `json:"status"`
	Checkpoint *S              `json:"checkpoint,omitempty"`
	On         []TaskId        `json:"on,omitempty"`
	Policy     JoinPolicy      `json:"policy,omitempty"`
	Outcome    *TaskOutcome[R] `json:"outcome,omitempty"`
}

// TaskRecord is the complete replacement record for one durable task state machine.
type TaskRecord[I, S, R any] struct {
	Id             TaskId         `json:"id"`
	ConversationId ConversationId `json:"conversationId"`
	// Kind is the registered task definition name.
	Kind string `json:"kind"`
	// Version is the definition version used to migrate live input and checkpoints.
	Version int `json:"version"`
	// Input is the original task input retained while the task is live or terminal.
	Input I `json:"input"`
	// Owner is the owning task of a child task; nil for a task its conversation owns. Immutable.
	Owner *TaskId `json:"owner,omitempty"`
	// Background reports whether this conversation-owned task is excluded from ordinary idle waits, conversation
	// aborts, and cascades.
	Background bool `json:"background"`
	// AbortRequested is the durable abort mark checked before run-mode progress is committed.
	AbortRequested bool            `json:"abortRequested"`
	State          TaskState[S, R] `json:"state"`
	// Memos are small first-writer-wins values retained while the task can run; nil once completing or terminal.
	Memos map[string]JsonValue `json:"memos,omitempty"`
}

// DocumentRecordScope is the owner of a document incarnation.
type DocumentRecordScope struct {
	Kind DocumentScope `json:"kind"`
	// ConversationId is set only for conversation scope.
	ConversationId ConversationId `json:"conversationId,omitempty"`
	// TaskId is set only for task scope.
	TaskId TaskId `json:"taskId,omitempty"`
}

// DocumentRecord is the persisted lifecycle record for one create-to-retire document incarnation.
type DocumentRecord struct {
	// Id is the unique incarnation ID; never reused when the same logical document is recreated.
	Id DocumentId `json:"id"`
	// Kind is the stable document definition kind.
	Kind string `json:"kind"`
	// Key is the family member key; nil for singleton documents.
	Key *string `json:"key,omitempty"`
	// CreatedAt is the commit that created the incarnation, stamped by storage.
	CreatedAt Seq `json:"createdAt"`
	// RetiredAt is the commit that retired the incarnation; nil while it is current.
	RetiredAt *Seq                `json:"retiredAt,omitempty"`
	Scope     DocumentRecordScope `json:"scope"`
	// History and Fork are set only for conversation scope.
	History DocumentHistory `json:"history,omitempty"`
	Fork    DocumentFork    `json:"fork,omitempty"`
}

// DocumentCreate holds the fields supplied when storage creates and stamps a new DocumentRecord.
type DocumentCreate struct {
	Id      DocumentId          `json:"id"`
	Kind    string              `json:"kind"`
	Key     *string             `json:"key,omitempty"`
	Scope   DocumentRecordScope `json:"scope"`
	History DocumentHistory     `json:"history,omitempty"`
	Fork    DocumentFork        `json:"fork,omitempty"`
}

// Page is one ordered scan result and its optional continuation state.
type Page[T, C any] struct {
	Items []T
	// Next is nil when the scan is complete.
	Next *C
}

// Cursor is backend-owned JSON continuation state that callers only round-trip to the same scan.
type Cursor = map[string]JsonValue

// ConversationQuery holds optional filters for an ordered conversation scan.
type ConversationQuery struct {
	OwnerConversationId *ConversationId
	OwnerTaskId         *TaskId
}

// EntryQuery holds inclusive ID bounds for a newest-first scan of one conversation's fork-aware history.
type EntryQuery struct {
	ConversationId ConversationId
	// MinEntryId is the oldest entry ID that may be returned.
	MinEntryId *EntryId
	// MaxEntryId is the newest entry ID that may be returned.
	MaxEntryId *EntryId
}

// TaskQuery holds optional filters for an ordered scan of durable task records.
type TaskQuery struct {
	ConversationId *ConversationId
	Kind           *string
	Status         *TaskStatus
	AbortRequested *bool
	Background     *bool
}

// SubmissionQuery holds optional filters for an ordered scan of submission records.
type SubmissionQuery struct {
	ConversationId *ConversationId
	Status         *SubmissionStatus
}

// DocumentPoint is the current state (Current) or one historical commit sequence used for document membership and
// content reads.
type DocumentPoint struct {
	Current bool
	Seq     Seq
}

// CurrentPoint selects the current state.
var CurrentPoint = DocumentPoint{Current: true}

// AtSeq selects one historical commit sequence.
func AtSeq(seq Seq) DocumentPoint { return DocumentPoint{Seq: seq} }

// DocumentAddress is the exact logical identity of a singleton or one keyed family member.
type DocumentAddress struct {
	Kind  string
	Scope DocumentRecordScope
	// Key is nil for the singleton and selects one family member otherwise.
	Key *string
}

// DocumentQuery is an ordered scan of document incarnations alive in one exact scope at one point.
type DocumentQuery struct {
	Scope DocumentRecordScope
	At    DocumentPoint
	Kind  *string
}

// DocumentContentKind is "base" or "delta".
type DocumentContentKind string

const (
	ContentBase  DocumentContentKind = "base"
	ContentDelta DocumentContentKind = "delta"
)

// DocumentContent is a complete checkpoint (base, with Value) or a Chord operation batch (delta, with Ops) selected
// by the owning Session.
type DocumentContent struct {
	Version int                 `json:"version"`
	Kind    DocumentContentKind `json:"kind"`
	Value   JsonObject          `json:"value,omitempty"`
	Ops     []Op                `json:"ops,omitempty"`
}

// DocumentCopySource is the exact persisted source selected for a definition-free document copy.
type DocumentCopySource struct {
	Id DocumentId
	At DocumentPoint
}

// StoredDocument is the detached materialized value and stored definition version at a selected point.
type StoredDocument struct {
	Record  DocumentRecord
	Version int
	Value   JsonObject
	// DeltasSinceBase counts the deltas replayed after the selected base to materialize Value.
	DeltasSinceBase int
}

// StorageWrite is one record or document mutation in an atomic storage commit.
type StorageWrite interface {
	storageWriteType() string
}

// ConversationWrite persists a new conversation.
type ConversationWrite struct{ Value ConversationRecord }

// EntryWrite persists a new entry.
type EntryWrite struct{ Value EntryRecord }

// TaskWrite persists the complete replacement record of a task.
type TaskWrite struct {
	Value TaskRecord[JsonValue, JsonValue, JsonValue]
}

// SubmissionWrite persists the complete replacement record of a submission.
type SubmissionWrite struct{ Value SubmissionRecord }

// DocumentCreateWrite creates an incarnation from a complete base; Content.Kind is always base.
type DocumentCreateWrite struct {
	Record  DocumentCreate
	Content DocumentContent
}

// DocumentCopyWrite creates an incarnation as a definition-free copy of a persisted source.
type DocumentCopyWrite struct {
	Record DocumentCreate
	Source DocumentCopySource
}

// DocumentChangeWrite appends a base or delta to an incarnation.
type DocumentChangeWrite struct {
	Id      DocumentId
	Content DocumentContent
}

// DocumentRetireWrite retires an incarnation.
type DocumentRetireWrite struct{ Id DocumentId }

func (ConversationWrite) storageWriteType() string   { return "conversation" }
func (EntryWrite) storageWriteType() string          { return "entry" }
func (TaskWrite) storageWriteType() string           { return "task" }
func (SubmissionWrite) storageWriteType() string     { return "submission" }
func (DocumentCreateWrite) storageWriteType() string { return "document.create" }
func (DocumentCopyWrite) storageWriteType() string   { return "document.copy" }
func (DocumentChangeWrite) storageWriteType() string { return "document.change" }
func (DocumentRetireWrite) storageWriteType() string { return "document.retire" }

// StorageWriteType returns the upstream `type` discriminator of a write.
func StorageWriteType(write StorageWrite) string { return write.storageWriteType() }

// CommitChange is a TableCommitChange or a DocumentCommitChange. Change order within a publication is unspecified.
type CommitChange interface {
	commitChangeType() string
}

// TableCommitChange is a complete table record committed without another publication copy: a ConversationWrite,
// EntryWrite, TaskWrite, or SubmissionWrite.
type TableCommitChange interface {
	StorageWrite
	CommitChange
}

func (ConversationWrite) commitChangeType() string { return "conversation" }
func (EntryWrite) commitChangeType() string        { return "entry" }
func (TaskWrite) commitChangeType() string         { return "task" }
func (SubmissionWrite) commitChangeType() string   { return "submission" }

// DocumentChange is the committed change of one document incarnation.
type DocumentChange struct {
	Record DocumentRecord
	// ConversationId owns the document; task documents derive it from their task record. Nil only for Session
	// documents.
	ConversationId *ConversationId
	// Version is the definition version of Value; nil when this commit retired the incarnation.
	Version *int
	// Value is the exact adopted immutable revision, or nil when this commit retired the incarnation.
	Value JsonObject
	// Ops are the exact adopted operations for an ordinary update; empty for creation and retirement.
	Ops []Op
}

// DocumentCopyChange is a definition-free child initialization; consumers hydrate through state or watch acquisition.
type DocumentCopyChange struct {
	Record         DocumentRecord
	ConversationId ConversationId
	Source         DocumentCopySource
}

func (DocumentChange) commitChangeType() string     { return "document" }
func (DocumentCopyChange) commitChangeType() string { return "document.copy" }

// DocumentCommitChange is a DocumentChange or a DocumentCopyChange.
type DocumentCommitChange interface {
	CommitChange
	documentCommitChange()
}

func (DocumentChange) documentCommitChange()     {}
func (DocumentCopyChange) documentCommitChange() {}

// CommitChangeType returns the upstream `type` discriminator of a change.
func CommitChangeType(change CommitChange) string { return change.commitChangeType() }

// CommitPublication holds every immutable change from one successful Session commit.
type CommitPublication struct {
	Seq     Seq
	Changes []CommitChange
}

// CreateConversationOptions selects the ownership of a created or forked conversation.
type CreateConversationOptions struct {
	Ownership ConversationOwnership
}

// Tx is the transaction surface of one Session commit callback. Table reads and creation results are trusted
// immutable values and may be shared with internal commit state.
//
// Upstream overloads doc, retireDoc, entry, appendEntry, and createTask by token type; Go methods cannot take type
// parameters, so the methods take erased tokens and the generic helpers (TxDoc, TxEntry, TxAppendEntry, CreateTask)
// restore the types.
type Tx interface {
	// Conversation returns nil when absent.
	Conversation(id ConversationId) (*ConversationRecord, error)
	// Entry returns nil when absent.
	Entry(id EntryId) (*EntryRecord, error)
	// Task returns nil when absent.
	Task(id TaskId) (*TaskRecord[JsonValue, JsonValue, JsonValue], error)
	ScanConversations(query ConversationQuery, limit int, cursor Cursor) (Page[ConversationRecord, Cursor], error)
	ScanEntries(query EntryQuery, limit int, cursor Cursor) (Page[EntryRecord, Cursor], error)
	// LatestHeadMarker returns the newest visible entry of the conversation that carries a Head; nil when none.
	LatestHeadMarker(conversationId ConversationId) (*EntryRecord, error)
	ScanTasks(query TaskQuery, limit int, cursor Cursor) (Page[TaskRecord[JsonValue, JsonValue, JsonValue], Cursor], error)
	// SubmissionByRequest returns the committed submission with a conversation-scoped request ID; nil when absent.
	SubmissionByRequest(conversationId ConversationId, requestId string) (*SubmissionRecord, error)

	// CreateConversation creates a conversation with explicitly selected ownership.
	CreateConversation(options CreateConversationOptions) (ConversationRecord, error)
	// ForkConversation creates a history fork at one concrete visible entry with explicitly selected ownership.
	ForkConversation(parentConversationId ConversationId, at EntryId, options CreateConversationOptions) (ConversationRecord, error)
	// AppendEntry returns a Session-owned immutable record that may be shared with commit listeners.
	AppendEntry(conversationId ConversationId, value EntryDraft) (EntryRecord, error)
	// CreateTaskErased creates a task of an erased definition with an already JSON-encoded input.
	CreateTaskErased(task AnyTask, input JsonValue, options TaskOptions) (TaskId, error)
	// CreateSubmission creates a raw submission record with a fresh ID. No admission rules apply: no busy check, no
	// inbox queueing, no placement. Use Conversation.Submit or a conversation handle unless the caller implements
	// admission itself.
	CreateSubmission(create SubmissionCreate) (SubmissionRecord, error)
	// SettleSubmission settles a queued or placed submission; only a placed input can be answered, and a settled
	// submission stays unchanged. Resolved against this transaction's latest record of the submission, so it works
	// after table writes. Run tasks settle the inputs they answer.
	SettleSubmission(id SubmissionId, settlement SubmissionSettlement) error
	// PlaceSubmission places a queued submission at entry: an input becomes placed, a write done. Resolved like
	// SettleSubmission. Inbox boundaries place the submissions they select.
	PlaceSubmission(id SubmissionId, entry EntryId) error

	// Doc returns the draft of a document, creating it when absent. args are the owner ID (conversation or task
	// scope), then the family key and seed (families). A family access without a seed fails as upstream's undefined
	// seed does.
	Doc(token AnyDocToken, args ...any) (*delta.Object, error)
	// RetireDoc retires a document; args are the owner ID and family key as for Doc, without a seed.
	RetireDoc(token AnyDocToken, args ...any) error
}

// DocumentState is disposable, read-only Chord state bound to one committed document incarnation of type T; its
// value is nil once the incarnation is retired. The state holds the JSON value (T is phantom, as for Draft);
// DecodeDoc[T](state.Value()) reads it as T.
type DocumentState[T any] = *chord.AttachedReplicatedState[JsonObject]

// AttachedReplicatedState is a synchronously hydrated publication-only Chord state backed by one source attachment.
type AttachedReplicatedState[T any] = *chord.AttachedReplicatedState[T]

// WatchEndReason is why a watch ended.
type WatchEndReason string

const (
	WatchStopped       WatchEndReason = "stopped"
	WatchCancelled     WatchEndReason = "cancelled"
	WatchSessionClosed WatchEndReason = "session_closed"
	WatchRetired       WatchEndReason = "retired"
	WatchListenerError WatchEndReason = "listener_error"
)

// WatchEnd is the terminal result of one document watch; Error is set only for listener_error.
type WatchEnd struct {
	Reason WatchEndReason
	Error  error
}

// WatchHandle is a serialized exact-frame observation of an immutable value with bounded pending delivery.
type WatchHandle[T any] interface {
	// Value is the acquisition revision before Start and the latest delivered immutable revision afterward.
	Value() T
	// Start installs the sole asynchronous listener. It never invokes it inline.
	Start(listener func(ctx context.Context, value T, ops []Op) error)
	// Stop idempotently stops future callbacks and returns this watch's terminal result.
	Stop() (WatchEnd, error)
	// Closed returns a channel closed when the watch terminates and the terminal result; an already-running callback
	// remains caller-owned.
	Closed() <-chan struct{}
	// End returns the terminal result once Closed is closed.
	End() WatchEnd
}

// DocumentWatch is a watch of one document incarnation; the value is nil once retired.
type DocumentWatch[T any] = WatchHandle[*T]

// DocumentReader holds committed document reads (Session.snapshot and snapshotAsOf).
type DocumentReader interface {
	// Snapshot returns the committed value of a document as JSON, or nil when absent. args are the owner ID and family
	// key as for Tx.Doc. Typed reads use the Snapshot helper.
	SnapshotErased(ctx context.Context, token AnyDocToken, args ...any) (JsonObject, error)
	// SnapshotAsOfErased returns the committed value of a rewindable conversation document as of the visible entry at;
	// args are the conversation ID, then the family key for families.
	SnapshotAsOfErased(ctx context.Context, token AnyDocToken, at EntryId, args ...any) (JsonObject, error)
}

// DocumentObserver is the non-creating document watch acquisition shared by Session and invocation APIs.
type DocumentObserver interface {
	// WatchDocErased returns a watch of the document's JSON value, or nil when absent. Typed watches use WatchDoc.
	WatchDocErased(ctx context.Context, token AnyDocToken, args ...any) (WatchHandle[JsonObject], error)
}

// Session owns one mutation line, its records, and its tracked documents.
type Session interface {
	DocumentObserver
	DocumentReader
	// Commit runs one atomic transaction on the Session mutation line.
	Commit(ctx context.Context, change func(tx Tx) (any, error)) (any, error)
	// Close seals admission, settles admitted commits, then closes storage.
	Close(ctx context.Context) error
	// SubscribeCommits observes complete commits synchronously after adoption. The listener must not panic, block,
	// or call Session APIs.
	SubscribeCommits(listener func(ctx context.Context, publication CommitPublication)) func()
	// SubscribeClose observes close synchronously when it begins. The listener must not panic, block, or call
	// Session APIs.
	SubscribeClose(listener func()) func()
	// DocumentStateErased returns a disposable state of the document's JSON value, or nil when absent. Typed states
	// use the DocumentStateOf helper.
	DocumentStateErased(ctx context.Context, token AnyDocToken, args ...any) (AttachedReplicatedState[JsonObject], error)
}

// Committer runs one atomic Session commit: a Session, a Conversation, or a tool's ToolExecutionApi.
type Committer interface {
	Commit(ctx context.Context, change func(tx Tx) (any, error)) (any, error)
}

// Commit runs change in one commit and returns its typed result.
func Commit[T any](ctx context.Context, committer Committer, change func(tx Tx) (T, error)) (T, error) {
	result, err := committer.Commit(ctx, func(tx Tx) (any, error) { return change(tx) })
	if err != nil {
		var zero T
		return zero, err
	}
	typed, _ := result.(T)
	return typed, nil
}

// EntryAt is an entry and the sequence of the commit that persisted it.
type EntryAt struct {
	Entry     EntryRecord
	CommitSeq Seq
}

// Storage is the atomic persistence boundary for Session records.
//
// Storage trusts the owning Session to supply semantically valid records, references, ancestry, and transitions.
// Implementations enforce atomicity, global ID ownership, immutable conversation/entry creation, document record
// consistency, and detached values; Session serializes commits.
type Storage interface {
	// Commit atomically persists one batch and returns its sequence. Once it returns, later reads through this
	// storage observe it.
	Commit(ctx context.Context, writes []StorageWrite) (Seq, error)
	// MintId returns a fresh candidate from the Session-global numeric ID namespace.
	MintId() (int64, error)
	// Conversation looks up one conversation by exact ID; nil when absent.
	Conversation(ctx context.Context, id ConversationId) (*ConversationRecord, error)
	// ScanConversations scans conversations in ascending ID order.
	ScanConversations(ctx context.Context, query ConversationQuery, limit int, cursor Cursor) (Page[ConversationRecord, Cursor], error)
	// Entry looks up one global entry and the sequence of the commit that persisted it; nil when absent.
	Entry(ctx context.Context, id EntryId) (*EntryAt, error)
	// VisibleEntry looks up one entry only when it is visible through the requested conversation's ancestry.
	VisibleEntry(ctx context.Context, conversationId ConversationId, id EntryId) (*EntryAt, error)
	// FindLatestHeadMarker returns the newest visible entry with a Head at or below the optional inclusive cutoff.
	// The returned entry is the marker; its Head is the range's actual lower bound.
	FindLatestHeadMarker(ctx context.Context, conversationId ConversationId, atOrBeforeEntryId *EntryId) (*EntryRecord, error)
	// ScanEntries scans the inclusive visible range newest-first, returning at most limit entries.
	ScanEntries(ctx context.Context, query EntryQuery, limit int, cursor Cursor) (Page[EntryRecord, Cursor], error)
	// Task looks up the latest complete record for one task; nil when absent.
	Task(ctx context.Context, id TaskId) (*TaskRecord[JsonValue, JsonValue, JsonValue], error)
	// ScanTasks scans task records matching every supplied filter.
	ScanTasks(ctx context.Context, query TaskQuery, limit int, cursor Cursor) (Page[TaskRecord[JsonValue, JsonValue, JsonValue], Cursor], error)
	// Submission looks up the latest complete record for one admitted submission; nil when absent.
	Submission(ctx context.Context, id SubmissionId) (*SubmissionRecord, error)
	// ScanSubmissions scans submissions matching every supplied filter in ascending ID order.
	ScanSubmissions(ctx context.Context, query SubmissionQuery, limit int, cursor Cursor) (Page[SubmissionRecord, Cursor], error)
	// SubmissionByRequest finds a submission by its conversation-scoped host deduplication key; nil when absent.
	SubmissionByRequest(ctx context.Context, conversationId ConversationId, requestId string) (*SubmissionRecord, error)
	// FindDocument resolves the incarnation occupying one exact logical address at the selected point; nil when
	// absent.
	FindDocument(ctx context.Context, address DocumentAddress, at DocumentPoint) (*DocumentRecord, error)
	// Document materializes one specific incarnation by ID at the selected point without following a replacement at
	// its address; nil when absent.
	Document(ctx context.Context, id DocumentId, at DocumentPoint) (*StoredDocument, error)
	// ScanDocuments scans incarnations alive in one exact scope at the selected point.
	ScanDocuments(ctx context.Context, query DocumentQuery, limit int, cursor Cursor) (Page[DocumentRecord, Cursor], error)
	// Close releases backend resources; all later operations must fail.
	Close(ctx context.Context) error
}
