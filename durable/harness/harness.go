// Ports packages/durable/src/harness/harness.ts.

package harness

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/session"
)

const harnessScanPageSize = 256

type createTargetKind uint8

const (
	createRoot createTargetKind = iota
	createIndependent
	createFork
)

type createTarget struct {
	kind      createTargetKind
	ownership durable.ConversationOwnership
	parentId  durable.ConversationId
	at        durable.EntryId
}

// conversationHost holds the Harness-private services used by Conversation handles.
type conversationHost struct {
	harness     *harnessImpl
	storage     durable.Storage
	tasks       *TaskScheduler
	submissions *Submissions
	views       *ConversationViews
	now         func() float64
}

type conversationImpl struct {
	id   durable.ConversationId
	host *conversationHost
}

func (conversation *conversationImpl) Id() durable.ConversationId { return conversation.id }

func (conversation *conversationImpl) Agent(ctx context.Context) (durable.Agent, error) {
	return conversation.host.harness.resolveAgent(ctx, conversation.id, nil)
}

func (conversation *conversationImpl) Configure(ctx context.Context, change AgentChange) error {
	_, err := conversation.host.harness.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
		return nil, Configure(tx, conversation.id, change)
	}, session.TransactionScope{})
	return err
}

func (conversation *conversationImpl) Submit(ctx context.Context, submission durable.SubmissionDraft) (durable.Submission, error) {
	return conversation.host.submissions.Submit(ctx, conversation.id, submission)
}

func (conversation *conversationImpl) Compact(ctx context.Context, instructions *string) (durable.TaskId, error) {
	conversation.host.tasks.Resume()
	input := CompactionInput{Reason: durable.CompactionManual, Instructions: instructions}
	result, err := conversation.host.harness.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
		return CreateCompaction(tx, conversation.id, input, nil)
	}, session.TransactionScope{})
	if err != nil {
		return 0, err
	}
	return result.(durable.TaskId), nil
}

func (conversation *conversationImpl) Reset(ctx context.Context, handoff *string) error {
	entry := durable.EntryDraft{Kind: durable.ResetEntry.Kind, HeadSelf: true}
	if handoff != nil {
		entry.Model = []ai.Message{ai.UserMessage{Content: ai.UserText(*handoff), Timestamp: int64(conversation.host.now())}}
	}
	_, err := conversation.host.submissions.Submit(ctx, conversation.id, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &entry})
	return err
}

func (conversation *conversationImpl) Commit(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error) {
	id := conversation.id
	return conversation.host.harness.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
		return change(tx)
	}, session.TransactionScope{ConversationId: &id})
}

func (conversation *conversationImpl) Context(ctx context.Context) (durable.ContextView, error) {
	return ReadContext(ctx, conversation.host.harness.line, conversation.host.storage, conversation.id, nil)
}

func (conversation *conversationImpl) Entries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	bounded := durable.EntryQuery{ConversationId: conversation.id, MinEntryId: query.MinEntryId, MaxEntryId: query.MaxEntryId}
	page, err := conversation.host.harness.ReadOnLine(func() (any, error) {
		return conversation.host.storage.ScanEntries(ctx, bounded, limit, cursor)
	})
	if err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	return page.(durable.Page[durable.EntryRecord, durable.Cursor]), nil
}

func (conversation *conversationImpl) Fork(ctx context.Context, at durable.EntryId, options ConversationCreateOptions) (Conversation, error) {
	target := createTarget{kind: createFork, parentId: conversation.id, at: at, ownership: options.Ownership}
	return conversation.host.harness.create(ctx, target, RootOptions{Agent: options.Agent, Init: options.Init})
}

func (conversation *conversationImpl) Abort(ctx context.Context, options *durable.ConversationAbortOptions) error {
	conversation.host.tasks.Resume()
	return conversation.host.tasks.AbortConversation(ctx, conversation.id, options != nil && options.Background)
}

func (conversation *conversationImpl) WaitForIdle(ctx context.Context) error {
	conversation.host.tasks.Resume()
	id := conversation.id
	return conversation.host.tasks.WaitForIdle(ctx, &id)
}

func (conversation *conversationImpl) ViewState(ctx context.Context) (durable.AttachedReplicatedState[ConversationView], error) {
	return conversation.host.views.State(ctx, conversation.id)
}

func (conversation *conversationImpl) Watch(ctx context.Context) (ConversationWatch, error) {
	return conversation.host.views.Watch(ctx, conversation.id)
}

// harnessImpl is the Session kernel extended with conversation handles and a registry.
type harnessImpl struct {
	*session.SessionImpl
	line        sessionLine
	storage     durable.Storage
	options     HarnessOptions
	report      func(error)
	host        *conversationHost
	tasks       *TaskScheduler
	submissions *Submissions
	taskGraph   *TaskGraphView
	closed      atomic.Bool
}

func (harness *harnessImpl) settings() durable.Settings {
	if harness.options.Settings == nil {
		return ResolveSettings(nil)
	}
	return ResolveSettings(harness.options.Settings())
}

// resolveAgent resolves a conversation's committed pi.agent against snapshot, or the current one, and the current settings.
func (harness *harnessImpl) resolveAgent(ctx context.Context, id durable.ConversationId, snapshot durable.RegistrySnapshot) (durable.Agent, error) {
	registry := snapshot
	if registry == nil {
		registry = harness.options.Registry.Snapshot()
	}
	state, err := durable.Snapshot[AgentState](ctx, harness, AgentDoc, id)
	if err != nil {
		return durable.Agent{}, err
	}
	return ResolveAgent(state, registry, harness.settings(), harness.report), nil
}

// buildEnv builds a conversation's environment from its current cwd; nil without an Env option.
func (harness *harnessImpl) buildEnv(ctx context.Context, id durable.ConversationId) (env.ExecutionEnv, error) {
	build := harness.options.Env
	if build == nil {
		return nil, nil
	}
	state, err := durable.Snapshot[AgentState](ctx, harness, AgentDoc, id)
	if err != nil {
		return nil, err
	}
	target := EnvTarget{ConversationId: id, Read: harness}
	if state != nil {
		target.Cwd = state.Cwd
	}
	return build(ctx, target)
}

func (harness *harnessImpl) Resume() {
	harness.assertOpen()
	harness.tasks.Resume()
}

func (harness *harnessImpl) GetTask(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	record, err := harness.ReadOnLine(func() (any, error) { return harness.storage.Task(ctx, id) })
	if err != nil {
		return nil, err
	}
	return record.(*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]), nil
}

func (harness *harnessImpl) Inspect(ctx context.Context) (HarnessInspection, error) {
	result, err := harness.ReadOnLine(func() (any, error) {
		scheduling, tasks, err := harness.tasks.Inspect(harness.options.Registry.Snapshot())
		if err != nil {
			return nil, err
		}
		scan := func(status durable.SubmissionStatus) ([]durable.SubmissionRecord, error) {
			return ScanAll(func(cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
				return harness.storage.ScanSubmissions(ctx, durable.SubmissionQuery{Status: &status}, harnessScanPageSize, cursor)
			})
		}
		queued, err := scan(durable.SubmissionQueued)
		if err != nil {
			return nil, err
		}
		placed, err := scan(durable.SubmissionPlaced)
		if err != nil {
			return nil, err
		}
		submissions := slices.Concat(queued, placed)
		slices.SortStableFunc(submissions, func(a, b durable.SubmissionRecord) int { return int(a.Id - b.Id) })
		if submissions == nil {
			submissions = []durable.SubmissionRecord{}
		}
		return HarnessInspection{Scheduling: scheduling, Tasks: tasks, Submissions: submissions}, nil
	})
	if err != nil {
		return HarnessInspection{}, err
	}
	return result.(HarnessInspection), nil
}

func (harness *harnessImpl) Submission(ctx context.Context, id durable.SubmissionId) (durable.Submission, error) {
	return harness.submissions.Get(ctx, id)
}

func (harness *harnessImpl) AbortSubmission(ctx context.Context, id durable.SubmissionId, conversationId *durable.ConversationId) (durable.SubmissionAbortResult, error) {
	return harness.submissions.Abort(ctx, id, conversationId)
}

func (harness *harnessImpl) AbortTask(ctx context.Context, id durable.TaskId) (string, error) {
	return harness.tasks.Abort(ctx, id)
}

func (harness *harnessImpl) WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error) {
	harness.tasks.Resume()
	return harness.tasks.WaitForTask(ctx, id)
}

func (harness *harnessImpl) WaitForIdle(ctx context.Context) error {
	harness.tasks.Resume()
	return harness.tasks.WaitForIdle(ctx, nil)
}

// Usage sums every conversation's committed pi.usage. Each document is read at its own point; totals only grow.
func (harness *harnessImpl) Usage(ctx context.Context) (UsageState, error) {
	listed, err := harness.ReadOnLine(func() (any, error) {
		return ScanAll(func(cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
			return harness.storage.ScanConversations(ctx, durable.ConversationQuery{}, harnessScanPageSize, cursor)
		})
	})
	if err != nil {
		return UsageState{}, err
	}
	total := NewUsageState()
	for _, conversation := range listed.([]durable.ConversationRecord) {
		state, err := durable.Snapshot[UsageState](ctx, harness, UsageDoc, conversation.Id)
		if err != nil {
			return UsageState{}, err
		}
		if state != nil {
			AddUsageState(&total, *state)
		}
	}
	return total, nil
}

func (harness *harnessImpl) TaskGraph(ctx context.Context) (durable.AttachedReplicatedState[TaskGraph], error) {
	return harness.taskGraph.State(ctx)
}

func (harness *harnessImpl) WatchTaskGraph(ctx context.Context) (TaskGraphWatch, error) {
	return harness.taskGraph.Watch(ctx)
}

func (harness *harnessImpl) Root(ctx context.Context, options *RootOptions) (Conversation, error) {
	var create RootOptions
	if options != nil {
		create = *options
	}
	return harness.create(ctx, createTarget{kind: createRoot}, create)
}

func (harness *harnessImpl) Conversation(ctx context.Context, id durable.ConversationId) (Conversation, error) {
	if err := harness.checkOpen(); err != nil {
		return nil, err
	}
	record, err := harness.ReadOnLine(func() (any, error) { return harness.storage.Conversation(ctx, id) })
	if err != nil {
		return nil, err
	}
	found := record.(*durable.ConversationRecord)
	if found == nil {
		return nil, nil
	}
	return &conversationImpl{id: found.Id, host: harness.host}, nil
}

func (harness *harnessImpl) CreateConversation(ctx context.Context, options ConversationCreateOptions) (Conversation, error) {
	return harness.create(ctx, createTarget{kind: createIndependent, ownership: options.Ownership}, RootOptions{Agent: options.Agent, Init: options.Init})
}

// Close seals the Harness; later root, conversation, and creation calls fail with ErrClosed.
func (harness *harnessImpl) Close(ctx context.Context) error {
	harness.closed.Store(true)
	return harness.SessionImpl.Close(ctx)
}

func (harness *harnessImpl) create(ctx context.Context, target createTarget, options RootOptions) (Conversation, error) {
	if err := harness.checkOpen(); err != nil {
		return nil, err
	}
	result, err := harness.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
		if target.kind == createRoot {
			existing, err := tx.Conversation(durable.ROOT_CONVERSATION_ID)
			if err != nil {
				return nil, err
			}
			if existing != nil {
				return durable.ROOT_CONVERSATION_ID, nil
			}
		}
		var record durable.ConversationRecord
		var err error
		switch target.kind {
		case createRoot:
			record, err = tx.CreateRootConversation()
		case createFork:
			record, err = tx.ForkConversation(target.parentId, target.at, durable.CreateConversationOptions{Ownership: target.ownership})
		default:
			record, err = tx.CreateConversation(durable.CreateConversationOptions{Ownership: target.ownership})
		}
		if err != nil {
			return nil, err
		}
		if options.Agent != nil {
			if err := Configure(tx, record.Id, *options.Agent); err != nil {
				return nil, err
			}
		}
		if options.Init != nil {
			if err := options.Init(tx, record.Id); err != nil {
				return nil, err
			}
		}
		return record.Id, nil
	}, session.TransactionScope{})
	if err != nil {
		return nil, err
	}
	return &conversationImpl{id: result.(durable.ConversationId), host: harness.host}, nil
}

// conversationCreated is the built-in creation hook, in every commit that creates or forks a conversation: empty pi.live, pi.inbox, and pi.usage, a fresh pi.provider, the conversation's pi.agent (see CreateAgent), then HarnessOptions.ConversationCreated.
func (harness *harnessImpl) conversationCreated(tx *session.Transaction, record durable.ConversationRecord) error {
	for _, token := range []durable.AnyDocToken{LiveDoc, InboxDoc, UsageDoc, ProviderDoc} {
		if _, err := tx.Doc(token, record.Id); err != nil {
			return err
		}
	}
	if err := CreateAgent(tx, record); err != nil {
		return err
	}
	if created := harness.options.ConversationCreated; created != nil {
		return created(tx, record)
	}
	return nil
}

func (harness *harnessImpl) checkOpen() error {
	if harness.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (harness *harnessImpl) assertOpen() {
	if err := harness.checkOpen(); err != nil {
		panic(err)
	}
}

// boundConversation is the invocation-bound handle for tasks and tools. Every operation, and every operation of a submission it returns, first checks the invocation and runs under its signal, so it fails once the invocation ends; admitted work stays durable.
type boundConversation struct {
	id          durable.ConversationId
	binding     InvocationBinding
	submissions *Submissions
	tasks       *TaskScheduler
}

func (bound *boundConversation) bind(ctx context.Context) context.Context {
	return withSignal(ctx, bound.binding.Signal)
}

func (bound *boundConversation) Id() durable.ConversationId { return bound.id }

func (bound *boundConversation) Submit(ctx context.Context, draft durable.InputSubmissionDraft) (durable.Submission, error) {
	if err := bound.binding.Check(); err != nil {
		return nil, err
	}
	submission, err := bound.submissions.Submit(bound.bind(ctx), bound.id, draft)
	if err != nil {
		return nil, err
	}
	return &boundSubmission{inner: submission, bound: bound}, nil
}

func (bound *boundConversation) Abort(ctx context.Context, options *durable.ConversationAbortOptions) error {
	if err := bound.binding.Check(); err != nil {
		return err
	}
	return bound.tasks.AbortConversation(bound.bind(ctx), bound.id, options != nil && options.Background)
}

func (bound *boundConversation) WaitForIdle(ctx context.Context) error {
	if err := bound.binding.Check(); err != nil {
		return err
	}
	id := bound.id
	return bound.tasks.WaitForIdle(bound.bind(ctx), &id)
}

type boundSubmission struct {
	inner durable.Submission
	bound *boundConversation
}

func (submission *boundSubmission) Id() durable.SubmissionId { return submission.inner.Id() }

func (submission *boundSubmission) Status(ctx context.Context) (durable.SubmissionRecord, error) {
	if err := submission.bound.binding.Check(); err != nil {
		return durable.SubmissionRecord{}, err
	}
	return submission.inner.Status(submission.bound.bind(ctx))
}

func (submission *boundSubmission) Wait(ctx context.Context) (durable.SettledSubmissionRecord, error) {
	if err := submission.bound.binding.Check(); err != nil {
		return durable.SettledSubmissionRecord{}, err
	}
	return submission.inner.Wait(submission.bound.bind(ctx))
}

func (submission *boundSubmission) Abort(ctx context.Context) (durable.SubmissionAbortResult, error) {
	if err := submission.bound.binding.Check(); err != nil {
		return "", err
	}
	return submission.inner.Abort(submission.bound.bind(ctx))
}

// withSignal returns ctx additionally cancelled with signal's cause, as chord's withAbortSignal.
func withSignal(ctx context.Context, signal context.Context) context.Context {
	merged, release := linkedChild(ctx, signal)
	context.AfterFunc(merged, release)
	return merged
}

// OpenHarness opens a Harness over storage. The registry may keep changing while the Harness runs.
func OpenHarness(ctx context.Context, storage durable.Storage, options HarnessOptions) (Harness, error) {
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	snapshot := options.Registry.Snapshot()
	var missing []string
	for _, task := range BuiltinTasks() {
		if snapshot.Task(task.AnyDefinition().Name) == nil {
			missing = append(missing, task.AnyDefinition().Name)
		}
	}
	if len(missing) > 0 {
		return nil, errors.New("Registry lacks built-in tasks " + joinNames(missing) + "; create it with createRegistry()")
	}
	harness := newHarness(ctx, storage, options)
	if err := harness.tasks.Open(ctx); err != nil {
		// The caller's context may be what failed open: close without it, and rethrow the open error.
		if closeErr := harness.Close(context.WithoutCancel(ctx)); closeErr != nil && options.OnReport != nil {
			options.OnReport(closeErr)
		}
		return nil, err
	}
	return harness, nil
}

func joinNames(names []string) string {
	var joined strings.Builder
	for i, name := range names {
		if i > 0 {
			joined.WriteString(", ")
		}
		joined.WriteString(name)
	}
	return joined.String()
}

func newHarness(ctx context.Context, storage durable.Storage, options HarnessOptions) *harnessImpl {
	harness := &harnessImpl{storage: storage, options: options, report: options.OnReport}
	if harness.report == nil {
		harness.report = func(error) {}
	}
	now := options.Now
	if now == nil {
		now = func() float64 { return float64(time.Now().UnixMilli()) }
	}
	harness.SessionImpl = session.NewSessionImpl(storage, session.Hooks{
		ConversationCreated: harness.conversationCreated,
		// Join task invocations after admission is sealed and before Storage closes; writes no task outcome.
		BeforeClose: func() { harness.tasks.Join() },
	})
	harness.line = sessionLine{harness.SessionImpl}
	harness.tasks = NewTaskScheduler(TaskSchedulerOptions{
		Session:  harness.SessionImpl,
		Storage:  storage,
		Registry: options.Registry,
		Models:   options.Models,
		Agent: func(callCtx context.Context, id durable.ConversationId, snapshot durable.RegistrySnapshot) (durable.Agent, error) {
			return harness.resolveAgent(callCtx, id, snapshot)
		},
		Settings:       harness.settings,
		Env:            harness.buildEnv,
		Now:            now,
		Report:         harness.report,
		SettleOutcome:  SettleSchedulerOutcome,
		WithdrawInputs: WithdrawQueuedInputs,
		Conversation: func(callCtx context.Context, id durable.ConversationId, binding InvocationBinding) (durable.ConversationHandle, error) {
			record, err := harness.ReadOnLine(func() (any, error) { return storage.Conversation(callCtx, id) })
			if err != nil {
				return nil, err
			}
			if record.(*durable.ConversationRecord) == nil {
				return nil, nil
			}
			return &boundConversation{id: id, binding: binding, submissions: harness.submissions, tasks: harness.tasks}, nil
		},
		Context: context.WithoutCancel(ctx),
	})
	harness.submissions = NewSubmissions(harness.SessionImpl, storage, now, func() QueueModes { return QueueModesOf(harness.settings()) }, func() { harness.tasks.Resume() })
	harness.taskGraph = NewTaskGraphView(harness.SessionImpl, storage)
	harness.host = &conversationHost{
		harness:     harness,
		storage:     storage,
		tasks:       harness.tasks,
		submissions: harness.submissions,
		views:       NewConversationViews(harness.SessionImpl, storage),
		now:         now,
	}
	return harness
}

// sessionLine adapts the Session kernel to the context-taking line reader ReadContext declares.
type sessionLine struct{ *session.SessionImpl }

func (line sessionLine) ReadOnLine(_ context.Context, read func() (any, error)) (any, error) {
	return line.SessionImpl.ReadOnLine(read)
}
