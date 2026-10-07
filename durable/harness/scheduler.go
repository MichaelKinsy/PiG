// Ports packages/durable/src/harness/scheduler.ts.

package harness

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/session"
)

type anyTaskRecord = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
type anyTaskState = durable.TaskState[durable.JsonValue, durable.JsonValue]
type anyOutcome = durable.TaskOutcome[durable.JsonValue]

const (
	schedulerScanPageSize = 256
	// maxTimerDelay is the longest delay setTimeout supports; longer sleeps wait in several steps (scheduler.ts:54).
	maxTimerDelay = 2_147_483_647
)

var liveTaskStatuses = []durable.TaskStatus{durable.TaskPending, durable.TaskRunning, durable.TaskWaiting, durable.TaskCompleting}

// Blocked reasons: why a pending task cannot be reserved under a registry snapshot. Derived, never persisted.
const (
	blockedMissingTask     = "missing_task"
	blockedTaskTooOld      = "task_too_old"
	blockedMigrationFailed = "migration_failed"
)

type invocationMode int

const (
	modeRun invocationMode = iota
	modeAbort
)

// invocation is one in-memory execution of a task in run or abort mode.
type invocation struct {
	taskId         durable.TaskId
	conversationId durable.ConversationId
	mode           invocationMode
	// ctx is passed to handlers; cancel ends it with a cause.
	ctx    context.Context
	cancel context.CancelCauseFunc
	// watches acquired through the runtime stop at invocation end.
	watchMu sync.Mutex
	watches map[durable.WatchHandle[durable.JsonObject]]struct{}
	ended   atomic.Bool
	done    chan struct{}
}

func (invocation *invocation) endedError() error {
	return fmt.Errorf("Task %d invocation has ended", invocation.taskId)
}

// phaseState is what a runtime reads for the phase handler it serves: the phase's snapshot and task, and its lazily
// resolved agent.
type phaseState struct {
	mu       sync.Mutex
	snapshot durable.RegistrySnapshot
	task     durable.AnyTask
	agent    *agentResolution
	reported *reportedTask
}

type agentResolution struct {
	done  chan struct{}
	agent durable.Agent
	err   error
}

// reportedTask is the replacement definition already reported as unable to take over.
type reportedTask struct{ task durable.AnyTask }

type reservation struct {
	invocation *invocation
	task       durable.AnyTask
	snapshot   durable.RegistrySnapshot
}

type phaseResult struct {
	checkpoint durable.JsonValue
	failure    error
	failed     bool
}

// decision is the step decision: continue with the next phase, end the invocation, or end it by writing faulted.
type decision struct {
	proceed bool
	fault   error
}

// SchedulerOutcome is a terminal outcome the scheduler writes without running task code: faulted or orphaned.
type SchedulerOutcome = anyOutcome

// InvocationBinding is an invocation a conversation handle is bound to: its signal, and a check that fails once it
// ended.
type InvocationBinding struct {
	Signal context.Context
	Check  func() error
}

// taskNode holds the immutable ownership fields of a task.
type taskNode struct {
	conversationId durable.ConversationId
	owner          *durable.TaskId
	background     bool
}

// up is where a walk up the ownership tree continues: an owner task, or a conversation.
type up struct {
	isTask       bool
	task         durable.TaskId
	conversation durable.ConversationId
}

// step is one step of a walk up: an owner task with its node, a conversation, or an owner edge not loaded yet.
type step struct {
	kind         int
	task         durable.TaskId
	node         taskNode
	conversation durable.ConversationId
}

const (
	stepOwnerTask = iota
	stepOwnerConversation
	stepOwnerUnknown
)

// overlay holds candidate records a commit staged; they override committed records in ownership walks.
type overlay struct {
	tasks map[durable.TaskId]anyTaskRecord
	edges map[durable.ConversationId]*durable.TaskId
}

// scope is where ordinary ownership traversal starts: one conversation, or every ownerless conversation.
type scope struct {
	roots        bool
	conversation durable.ConversationId
}

// TaskSchedulerOptions configures NewTaskScheduler (scheduler.ts:134-160).
type TaskSchedulerOptions struct {
	Session  *session.SessionImpl
	Storage  durable.Storage
	Registry RegistryReader
	Models   durable.Models
	// Agent resolves a conversation's agent against a snapshot; the runtime calls it at most once per phase.
	Agent func(ctx context.Context, conversationId durable.ConversationId, snapshot durable.RegistrySnapshot) (durable.Agent, error)
	// Settings resolves the settings; read at each access.
	Settings func() durable.Settings
	// Env builds a conversation's environment with HarnessOptions.Env.
	Env    func(ctx context.Context, conversationId durable.ConversationId) (env.ExecutionEnv, error)
	Now    func() float64
	Report func(err error)
	// SettleOutcome is the Harness cleanup staged in the commit that makes an outcome the scheduler wrote itself
	// terminal.
	SettleOutcome func(tx durable.Tx, record anyTaskRecord, outcome SchedulerOutcome) error
	// WithdrawInputs withdraws a conversation's queued inputs, for conversation abort and abort cascades.
	WithdrawInputs func(tx durable.Tx, conversationId durable.ConversationId) error
	// Conversation returns an invocation-bound handle of an existing conversation, for task runtimes and tools.
	Conversation func(ctx context.Context, id durable.ConversationId, binding InvocationBinding) (durable.ConversationHandle, error)
	// Context is the context of scheduler commits and invocations; it carries no caller cancellation.
	Context context.Context
}

// TaskScheduler is the durable task scheduler of one Harness (scheduler.ts:162-1243).
//
// live mirrors every committed non-terminal task record: pending, running, waiting, and completing. The synchronous
// commit listener updates it on the Session line, so code running on the line reads exactly the committed state from
// it.
//
// Tasks and conversations form one ownership tree (spec §5.5): a task's parent is its owner task, or its
// conversation; a conversation's parent is its owner task, if any. Walks up that tree decide cascades, idle scopes, and
// whether a task's ordinary owned work is live, which holds its outcome as completing and delays its abort handler.
//
// Every task transition is decided and written by one callback serialized on the Session line: reservation, marks,
// runtime commits, finalization, and the step before each phase. Handlers and joins run off the line. An invocation
// ends inside the step that decides its end, so a runtime commit it queued either lands before that decision or is
// rejected. mu guards the in-memory state; it is never held while waiting for the line.
type TaskScheduler struct {
	options TaskSchedulerOptions

	mu                 sync.Mutex
	live               map[durable.TaskId]anyTaskRecord
	liveOrder          []durable.TaskId
	invocations        map[durable.TaskId]*invocation
	failedMigrations   map[durable.TaskId]failedMigration
	edges              map[durable.ConversationId]*durable.TaskId
	conversationOwners map[durable.TaskId]bool
	settled            map[durable.TaskId]taskNode
	failFastChecks     map[durable.TaskId]bool
	reconcileScheduled bool
	// reconciles counts passes scheduled or running; reconcilesIdle is closed when it returns to zero.
	reconciles     int
	reconcilesIdle chan struct{}
	cascadePending bool
	enabled        bool
	closing        bool
	sealing        atomic.Bool
	sealed         chan struct{}
	sealOnce       sync.Once
	dirty          bool
	draining       bool

	unsubscribeRegistry func()
	taskWaiters         Waiters[durable.TaskId, durable.SettledTask[durable.JsonValue]]
	// idleWaiters waits by conversation; idleHarness waits for the whole Harness.
	idleWaiters Waiters[durable.ConversationId, struct{}]
	idleHarness Waiters[bool, struct{}]
	background  sync.WaitGroup
}

type failedMigration struct {
	task durable.AnyTask
	err  error
}

// NewTaskScheduler returns a scheduler; Open loads it.
func NewTaskScheduler(options TaskSchedulerOptions) *TaskScheduler {
	return &TaskScheduler{
		options:             options,
		sealed:              make(chan struct{}),
		live:                map[durable.TaskId]anyTaskRecord{},
		invocations:         map[durable.TaskId]*invocation{},
		failedMigrations:    map[durable.TaskId]failedMigration{},
		edges:               map[durable.ConversationId]*durable.TaskId{},
		conversationOwners:  map[durable.TaskId]bool{},
		settled:             map[durable.TaskId]taskNode{},
		failFastChecks:      map[durable.TaskId]bool{},
		unsubscribeRegistry: func() {},
	}
}

// setLiveLocked records a live task, keeping first-insertion order like a JavaScript Map.
func (s *TaskScheduler) setLiveLocked(record anyTaskRecord) {
	if _, exists := s.live[record.Id]; !exists {
		s.liveOrder = append(s.liveOrder, record.Id)
	}
	s.live[record.Id] = record
}

func (s *TaskScheduler) deleteLiveLocked(id durable.TaskId) {
	if _, exists := s.live[id]; !exists {
		return
	}
	delete(s.live, id)
	if index := slices.Index(s.liveOrder, id); index >= 0 {
		s.liveOrder = slices.Delete(s.liveOrder, index, index+1)
	}
}

// liveValuesLocked returns the live records in insertion order.
func (s *TaskScheduler) liveValuesLocked() []anyTaskRecord {
	records := make([]anyTaskRecord, 0, len(s.liveOrder))
	for _, id := range s.liveOrder {
		records = append(records, s.live[id])
	}
	return records
}

// Open loads live tasks and changes surviving running tasks back to pending. It dispatches nothing.
func (s *TaskScheduler) Open(ctx context.Context) error {
	s.options.Session.SubscribeCommits(func(_ context.Context, publication durable.CommitPublication) { s.observe(publication) })
	s.options.Session.SubscribeClose(s.seal)
	s.unsubscribeRegistry = s.options.Registry.Subscribe(s.kick)
	_, err := s.options.Session.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
		// Every table read before the first write.
		var scans [][]anyTaskRecord
		for _, status := range liveTaskStatuses {
			records, err := ScanAll(func(cursor durable.Cursor) (durable.Page[anyTaskRecord, durable.Cursor], error) {
				return tx.ScanTasks(durable.TaskQuery{Status: &status}, schedulerScanPageSize, cursor)
			})
			if err != nil {
				return nil, err
			}
			scans = append(scans, records)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, records := range scans {
			for _, record := range records {
				s.setLiveLocked(record)
				if record.State.Status == durable.TaskRunning {
					if err := setTask(tx, withState(record, anyTaskState{Status: durable.TaskPending, Checkpoint: record.State.Checkpoint})); err != nil {
						return nil, err
					}
				}
				if record.State.Status == durable.TaskWaiting && record.State.Policy == durable.JoinFailFast {
					s.failFastChecks[record.Id] = true
				}
			}
		}
		return nil, nil
	}, session.TransactionScope{})
	if err != nil {
		return err
	}
	// Derive abort marks a crash left unapplied below cancelled owners, and finalize held outcomes.
	s.mu.Lock()
	s.cascadePending = true
	s.scheduleReconcileLocked()
	s.mu.Unlock()
	return nil
}

// Resume enables scheduling. It is idempotent; the kick does nothing once closing.
func (s *TaskScheduler) Resume() {
	s.mu.Lock()
	s.enabled = true
	s.mu.Unlock()
	s.kick()
}

// Join waits for every invocation signalled by the close seal and for scheduler passes in flight. It writes nothing.
func (s *TaskScheduler) Join() {
	// Session.Close runs the close listeners before BeforeClose (session.ts close), so the seal has normally run. Waiting
	// for it keeps Join from waiting on the background group before seal stops new passes for any other caller.
	<-s.sealed
	s.mu.Lock()
	var done []chan struct{}
	for _, invocation := range s.invocations {
		done = append(done, invocation.done)
	}
	s.mu.Unlock()
	for _, channel := range done {
		<-channel
	}
	s.background.Wait()
}

// Abort commits the abort mark, or settles a task that no registered definition can take as orphaned when nothing it
// owns is live, then joins the run invocation seen on the line; the commit listener signalled it. The abort invocation
// starts once the task's ordinary owned work is gone. A completing task is only marked (scheduler.ts:269-296).
func (s *TaskScheduler) Abort(ctx context.Context, id durable.TaskId) (string, error) {
	type marked struct {
		result string
		run    *invocation
	}
	result, err := commitOnLine(ctx, s.options.Session, session.TransactionScope{}, func(tx *session.Transaction) (marked, error) {
		current, err := tx.Task(id)
		if err != nil {
			return marked{}, err
		}
		if current == nil {
			return marked{}, fmt.Errorf("Task %d does not exist", id)
		}
		if current.State.Status == durable.TaskTerminal {
			return marked{result: "terminal"}, nil
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		running := s.invocations[id]
		if running == nil && current.State.Status != durable.TaskCompleting {
			if _, err := s.loadScopesLocked(false); err != nil {
				return marked{}, err
			}
			if _, owns := s.ownedLiveLocked(nil)[id]; !owns {
				resolution := s.resolveLocked(*current, s.options.Registry.Snapshot())
				if resolution.blocked != "" {
					reason := resolution.blocked
					if err := s.terminateLocked(tx, *current, anyOutcome{Status: durable.OutcomeOrphaned, Reason: &reason}); err != nil {
						return marked{}, err
					}
					return marked{result: "marked"}, nil
				}
			}
		}
		if !current.AbortRequested {
			record := *current
			record.AbortRequested = true
			if err := setTask(tx, record); err != nil {
				return marked{}, err
			}
		}
		if running != nil && running.mode == modeRun {
			return marked{result: "marked", run: running}, nil
		}
		return marked{result: "marked"}, nil
	})
	if err != nil {
		return "", err
	}
	// The commit listener signalled the run; join it. Like awaitWithContext, a caller already cancelled is rejected
	// even when the run has ended.
	if result.run != nil {
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		select {
		case <-result.run.done:
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
	}
	return result.result, nil
}

// WaitForTask returns the terminal receipt of a task; cancelling ctx cancels only this wait.
func (s *TaskScheduler) WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error) {
	// Check and register on the line so no terminal publication falls between them.
	type found struct {
		record *anyTaskRecord
		waiter *Waiter[durable.SettledTask[durable.JsonValue]]
	}
	result, err := readOnLine(s.options.Session, func() (found, error) {
		s.mu.Lock()
		closing := s.closing
		_, live := s.live[id]
		var waiter *Waiter[durable.SettledTask[durable.JsonValue]]
		if !closing && live {
			waiter = s.taskWaiters.Add(ctx, id)
		}
		s.mu.Unlock()
		if closing {
			return found{}, closedError()
		}
		if waiter != nil {
			return found{waiter: waiter}, nil
		}
		record, err := s.options.Storage.Task(ctx, id)
		if err != nil {
			return found{}, err
		}
		if record == nil {
			return found{}, fmt.Errorf("Task %d does not exist", id)
		}
		return found{record: record}, nil
	})
	if err != nil {
		return anyTaskRecord{}, err
	}
	if result.record != nil {
		return *result.record, nil
	}
	return result.waiter.Wait()
}

// WaitForIdle returns when ordinary traversal from the conversation, or from every ownerless conversation when
// conversationId is nil, reaches no live non-background task.
func (s *TaskScheduler) WaitForIdle(ctx context.Context, conversationId *durable.ConversationId) error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return closedError()
	}
	if s.idleLocked(conversationId) {
		s.mu.Unlock()
		return nil
	}
	s.scheduleReconcileLocked()
	var waiter *Waiter[struct{}]
	if conversationId == nil {
		waiter = s.idleHarness.Add(ctx, true)
	} else {
		waiter = s.idleWaiters.Add(ctx, *conversationId)
	}
	s.mu.Unlock()
	_, err := waiter.Wait()
	return err
}

// AbortConversation is Conversation.Abort: in one commit, withdraw the queued inputs and mark every live
// non-background task that ordinary traversal from the conversation reaches; it returns once the scope is idle. With
// background, traversal crosses background boundaries, and the wait also covers every task it reached
// (scheduler.ts:318-338).
func (s *TaskScheduler) AbortConversation(ctx context.Context, conversationId durable.ConversationId, background bool) error {
	reached, err := commitOnLine(ctx, s.options.Session, session.TransactionScope{}, func(tx *session.Transaction) ([]durable.TaskId, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		queued, err := s.loadScopesLocked(true)
		if err != nil {
			return nil, err
		}
		target := scope{conversation: conversationId}
		var reached []durable.TaskId
		for _, record := range s.liveValuesLocked() {
			if record.Background && !background {
				continue
			}
			if inScope, known := s.inScopeLocked(parentOf(nodeOf(record)), target, background); !known || !inScope {
				continue
			}
			reached = append(reached, record.Id)
			if !record.AbortRequested {
				record.AbortRequested = true
				if err := setTask(tx, record); err != nil {
					return nil, err
				}
			}
		}
		for _, id := range queued {
			if inScope, known := s.inScopeLocked(up{conversation: id}, target, background); known && inScope {
				if err := s.options.WithdrawInputs(tx, id); err != nil {
					return nil, err
				}
			}
		}
		return reached, nil
	})
	if err != nil {
		return err
	}
	if background {
		for _, id := range reached {
			if _, err := s.WaitForTask(ctx, id); err != nil {
				return err
			}
		}
	}
	return s.WaitForIdle(ctx, &conversationId)
}

// ─── Scheduling ────────────────────────────────────────────────────────

func (s *TaskScheduler) observe(publication durable.CommitPublication) {
	s.mu.Lock()
	var failed []durable.TaskId
	changed := false
	var updated []anyTaskRecord
	var terminal []anyTaskRecord
	for _, change := range publication.Changes {
		write, ok := change.(durable.TaskWrite)
		if !ok {
			continue
		}
		changed = true
		record := write.Value
		previous, hadPrevious := s.live[record.Id]
		if failedOutcome(record) && (!hadPrevious || !failedOutcome(previous)) {
			failed = append(failed, record.Id)
		}
		if record.State.Status == durable.TaskTerminal {
			s.deleteLiveLocked(record.Id)
			delete(s.failedMigrations, record.Id)
			delete(s.failFastChecks, record.Id)
			if s.conversationOwners[record.Id] {
				s.settled[record.Id] = nodeOf(record)
			}
			terminal = append(terminal, record)
			// Its owner may finalize now.
			s.scheduleReconcileLocked()
			continue
		}
		if record.AbortRequested && (!hadPrevious || !previous.AbortRequested) {
			s.cascadePending = true
			// Signal a run invocation of the newly marked task; its next step ends it.
			if running := s.invocations[record.Id]; running != nil && running.mode == modeRun {
				running.cancel(context.Canceled)
			}
		}
		status := record.State.Status
		if status == durable.TaskCompleting && (!hadPrevious || previous.State.Status != durable.TaskCompleting) {
			if cancellationIntent(record) {
				s.cascadePending = true
			}
			s.scheduleReconcileLocked()
		}
		if status == durable.TaskWaiting && record.State.Policy == durable.JoinFailFast && (!hadPrevious || previous.State.Status != durable.TaskWaiting) {
			s.failFastChecks[record.Id] = true
			s.scheduleReconcileLocked()
		}
		s.setLiveLocked(record)
		updated = append(updated, record)
	}
	for _, id := range failed {
		for _, record := range s.liveValuesLocked() {
			if record.State.Status == durable.TaskWaiting && record.State.Policy == durable.JoinFailFast && slices.Contains(record.State.On, id) {
				s.failFastChecks[record.Id] = true
				s.scheduleReconcileLocked()
			}
		}
	}
	for _, change := range publication.Changes {
		if write, ok := change.(durable.ConversationWrite); ok {
			if _, known := s.edges[write.Value.Id]; !known {
				s.setEdgeLocked(write.Value.Id, ownerTaskOf(write.Value))
			}
		}
	}
	for _, change := range publication.Changes {
		// A queued input below a cancelled owner is withdrawn, even after its cascade.
		write, ok := change.(durable.SubmissionWrite)
		if !ok || write.Value.Status != durable.SubmissionQueued || write.Value.Type != durable.SubmissionTypeInput {
			continue
		}
		start := up{conversation: write.Value.ConversationId}
		if !s.chainKnownLocked(start, nil) || s.belowCancelledLocked(start) {
			s.cascadePending = true
		}
	}
	for _, record := range updated {
		// Work created below a cancelled owner, even after its cascade, is aborted too.
		parent := parentOf(nodeOf(record))
		if !s.chainKnownLocked(parent, nil) {
			s.scheduleReconcileLocked()
		} else if !record.Background && !record.AbortRequested && s.belowCancelledLocked(parent) {
			s.cascadePending = true
		}
	}
	// Also retries, with the next commit of any kind, a cascade whose commit failed.
	if s.cascadePending {
		s.scheduleReconcileLocked()
	}
	s.mu.Unlock()
	for _, record := range terminal {
		s.taskWaiters.Resolve(record.Id, record)
	}
	if !changed {
		return
	}
	s.resolveIdleWaiters()
	s.kick()
}

func (s *TaskScheduler) resolveIdleWaiters() {
	s.mu.Lock()
	var idle []durable.ConversationId
	for _, conversationId := range s.idleWaiters.Keys() {
		if s.idleLocked(&conversationId) {
			idle = append(idle, conversationId)
		}
	}
	harness := len(s.idleHarness.Keys()) > 0 && s.idleLocked(nil)
	s.mu.Unlock()
	for _, conversationId := range idle {
		s.idleWaiters.Resolve(conversationId, struct{}{})
	}
	if harness {
		s.idleHarness.Resolve(true, struct{}{})
	}
}

// ─── Ownership ───────────────────────────────────────────────────────────

func (s *TaskScheduler) scheduleReconcileLocked() {
	if s.reconcileScheduled || s.closing {
		return
	}
	s.reconcileScheduled = true
	if s.reconciles == 0 {
		s.reconcilesIdle = make(chan struct{})
	}
	s.reconciles++
	s.background.Go(s.reconcile)
}

// awaitReconcilesLocked returns, with s.mu held, once no reconcile pass is scheduled or running. Upstream queues a
// reconcile as a microtask before the drain microtask that follows it, so the reconcile's commit enters the Session line
// first (scheduler.ts:430-432, 675-681); goroutines have no such order. A reservation that overtook a pending
// reconcile would run a task the reconcile is about to abort-mark, and a mark rejected by the Storage would then have
// no later commit to retry it. reserve skips a pass while a reconcile is owed for the same reason.
func (s *TaskScheduler) awaitReconcilesLocked() {
	for s.reconciles > 0 {
		idle := s.reconcilesIdle
		s.mu.Unlock()
		<-idle
		s.mu.Lock()
	}
}

// reconcile writes, in one commit, what committed records imply (scheduler.ts:445-485): abort marks below live owners
// with cancellation intent (spec §5.4), failFast marks (spec §5.5), withdrawn queued inputs below cancelled owners, and
// the final terminal record of every completing task whose ordinary owned work is gone. The durable records are the
// intent, so this also repairs whatever a crash left unapplied. It resolves idle waiters that the loaded edges decide.
func (s *TaskScheduler) reconcile() {
	defer s.endReconcile()
	s.mu.Lock()
	s.reconcileScheduled = false
	cascade := s.cascadePending
	s.cascadePending = false
	checks := slices.Sorted(maps.Keys(s.failFastChecks))
	clear(s.failFastChecks)
	s.mu.Unlock()
	_, err := s.options.Session.CommitWith(s.options.Context, func(tx *session.Transaction) (any, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closing {
			return nil, nil
		}
		queued, err := s.loadScopesLocked(cascade)
		if err != nil {
			return nil, err
		}
		marked := map[durable.TaskId]bool{}
		mark := func(record anyTaskRecord) error {
			if record.AbortRequested || marked[record.Id] {
				return nil
			}
			marked[record.Id] = true
			record.AbortRequested = true
			return setTask(tx, record)
		}
		// Loading edges can reveal a cancelled owner, so marks are derived on every pass.
		for _, record := range s.liveValuesLocked() {
			if !record.Background && s.belowCancelledLocked(parentOf(nodeOf(record))) {
				if err := mark(record); err != nil {
					return nil, err
				}
			}
		}
		for _, id := range checks {
			waiter, ok := s.live[id]
			if !ok || waiter.State.Status != durable.TaskWaiting {
				continue
			}
			anyFailed, err := s.anyFailedLocked(waiter.State.On)
			if err != nil {
				return nil, err
			}
			if !anyFailed {
				continue
			}
			// Every other live task: the failed one keeps its own outcome.
			for _, member := range waiter.State.On {
				if record, ok := s.live[member]; ok && !failedOutcome(record) {
					if err := mark(record); err != nil {
						return nil, err
					}
				}
			}
		}
		for _, id := range queued {
			if s.belowCancelledLocked(up{conversation: id}) {
				if err := s.options.WithdrawInputs(tx, id); err != nil {
					return nil, err
				}
			}
		}
		return nil, s.finalizeLocked(tx)
	}, session.TransactionScope{})
	if err != nil {
		// Any pass may have staged marks, so a failed one is retried with the next commit.
		s.mu.Lock()
		s.cascadePending = true
		for _, id := range checks {
			s.failFastChecks[id] = true
		}
		closing := s.closing
		s.mu.Unlock()
		if !closing {
			s.options.Report(err)
		}
	}
	s.resolveIdleWaiters()
}

func (s *TaskScheduler) endReconcile() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconciles--
	if s.reconciles == 0 {
		close(s.reconcilesIdle)
	}
}

// anyFailedLocked reports whether any of ids holds or ended with an outcome other than completed.
func (s *TaskScheduler) anyFailedLocked(ids []durable.TaskId) (bool, error) {
	for _, id := range ids {
		record, ok := s.live[id]
		if !ok {
			stored, err := s.options.Storage.Task(s.options.Context, id)
			if err != nil {
				return false, err
			}
			if stored == nil {
				continue
			}
			record = *stored
		}
		if failedOutcome(record) {
			return true, nil
		}
	}
	return false, nil
}

// finalizeLocked writes the terminal record of every completing task without live ordinary owned work. Finalizing one
// can free its owner, so this repeats over the commit's candidates until nothing changes. A held scheduler outcome
// gets its Harness cleanup here.
func (s *TaskScheduler) finalizeLocked(tx *session.Transaction) error {
	for {
		staged := overlayOf(tx)
		owned := s.ownedLiveLocked(staged)
		var done []anyTaskRecord
		for _, record := range s.liveRecordsLocked(staged) {
			if _, holds := owned[record.Id]; record.State.Status == durable.TaskCompleting && !holds {
				done = append(done, record)
			}
		}
		if len(done) == 0 {
			return nil
		}
		for _, record := range done {
			outcome := *record.State.Outcome
			if err := setTask(tx, withState(record, anyTaskState{Status: durable.TaskTerminal, Outcome: &outcome})); err != nil {
				return err
			}
			// Only the scheduler writes faulted and orphaned (spec §5.4); their cleanup waits for this commit.
			if outcome.Status == durable.OutcomeFaulted || outcome.Status == durable.OutcomeOrphaned {
				if err := s.options.SettleOutcome(tx, record, outcome); err != nil {
					return err
				}
			}
		}
	}
}

// loadScopesLocked loads the owner chains of every live task and, with queued, of every conversation with queued
// submissions; it returns the latter. It reads committed Storage directly, so it may run inside a commit callback.
func (s *TaskScheduler) loadScopesLocked(queued bool) ([]durable.ConversationId, error) {
	for _, record := range s.liveValuesLocked() {
		parent := parentOf(nodeOf(record))
		if !s.chainKnownLocked(parent, nil) {
			if err := s.loadChainLocked(parent, nil); err != nil {
				return nil, err
			}
		}
	}
	if !queued {
		return nil, nil
	}
	status := durable.SubmissionQueued
	submissions, err := ScanAll(func(cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
		return s.options.Storage.ScanSubmissions(s.options.Context, durable.SubmissionQuery{Status: &status}, schedulerScanPageSize, cursor)
	})
	if err != nil {
		return nil, err
	}
	var conversations []durable.ConversationId
	seen := map[durable.ConversationId]bool{}
	for _, submission := range submissions {
		if !seen[submission.ConversationId] {
			seen[submission.ConversationId] = true
			conversations = append(conversations, submission.ConversationId)
		}
	}
	for _, id := range conversations {
		if err := s.loadChainLocked(up{conversation: id}, nil); err != nil {
			return nil, err
		}
	}
	return conversations, nil
}

// loadChainLocked loads the owner edges and task nodes from start up to its ownerless root.
func (s *TaskScheduler) loadChainLocked(start up, staged *overlay) error {
	at := &start
	for at != nil {
		if at.isTask {
			node, ok := s.nodeLocked(at.task, staged)
			if !ok {
				record, err := s.options.Storage.Task(s.options.Context, at.task)
				if err != nil {
					return err
				}
				if record == nil {
					return nil
				}
				node = nodeOf(*record)
				if record.State.Status == durable.TaskTerminal {
					s.settled[record.Id] = node
				}
			}
			next := parentOf(node)
			at = &next
			continue
		}
		edge, known := s.edgeLocked(at.conversation, staged)
		if !known {
			record, err := s.options.Storage.Conversation(s.options.Context, at.conversation)
			if err != nil {
				return err
			}
			if record != nil {
				edge = ownerTaskOf(*record)
			}
			s.setEdgeLocked(at.conversation, edge)
		}
		if edge == nil {
			at = nil
		} else {
			at = &up{isTask: true, task: *edge}
		}
	}
	return nil
}

func (s *TaskScheduler) setEdgeLocked(conversationId durable.ConversationId, owner *durable.TaskId) {
	s.edges[conversationId] = owner
	if owner != nil {
		s.conversationOwners[*owner] = true
	}
}

// edgeLocked returns the owner task of a conversation (nil when ownerless); known is false while not loaded.
func (s *TaskScheduler) edgeLocked(id durable.ConversationId, staged *overlay) (owner *durable.TaskId, known bool) {
	if staged != nil {
		if edge, ok := staged.edges[id]; ok {
			return edge, true
		}
	}
	edge, ok := s.edges[id]
	return edge, ok
}

func (s *TaskScheduler) nodeLocked(id durable.TaskId, staged *overlay) (taskNode, bool) {
	if staged != nil {
		if record, ok := staged.tasks[id]; ok {
			return nodeOf(record), true
		}
	}
	if record, ok := s.live[id]; ok {
		return nodeOf(record), true
	}
	node, ok := s.settled[id]
	return node, ok
}

// aboveLocked walks up from start: owner tasks and conversations, ending at an ownerless root or an edge not loaded
// yet.
func (s *TaskScheduler) aboveLocked(start up, staged *overlay) []step {
	var steps []step
	at := &start
	for at != nil {
		if at.isTask {
			node, ok := s.nodeLocked(at.task, staged)
			if !ok {
				return append(steps, step{kind: stepOwnerUnknown})
			}
			steps = append(steps, step{kind: stepOwnerTask, task: at.task, node: node})
			next := parentOf(node)
			at = &next
			continue
		}
		steps = append(steps, step{kind: stepOwnerConversation, conversation: at.conversation})
		edge, known := s.edgeLocked(at.conversation, staged)
		if !known {
			return append(steps, step{kind: stepOwnerUnknown})
		}
		if edge == nil {
			at = nil
		} else {
			at = &up{isTask: true, task: *edge}
		}
	}
	return steps
}

// chainKnownLocked reports whether every owner above start is loaded.
func (s *TaskScheduler) chainKnownLocked(start up, staged *overlay) bool {
	for _, each := range s.aboveLocked(start, staged) {
		if each.kind == stepOwnerUnknown {
			return false
		}
	}
	return true
}

// liveRecordsLocked returns the live records, with the overlay's candidates replacing committed ones; terminal
// candidates are gone.
func (s *TaskScheduler) liveRecordsLocked(staged *overlay) []anyTaskRecord {
	var records []anyTaskRecord
	for _, record := range s.liveValuesLocked() {
		candidate := record
		if staged != nil {
			if replacement, ok := staged.tasks[record.Id]; ok {
				candidate = replacement
			}
		}
		if candidate.State.Status != durable.TaskTerminal {
			records = append(records, candidate)
		}
	}
	if staged == nil {
		return records
	}
	for _, id := range slices.Sorted(maps.Keys(staged.tasks)) {
		record := staged.tasks[id]
		if _, live := s.live[id]; !live && record.State.Status != durable.TaskTerminal {
			records = append(records, record)
		}
	}
	return records
}

// ownedLiveLocked maps every task with live ordinary owned work (spec §5.5) to that work: each live non-background task
// counts for every owner task above it up to and including the first background one. Owner chains must be loaded.
func (s *TaskScheduler) ownedLiveLocked(staged *overlay) map[durable.TaskId][]durable.TaskId {
	owned := map[durable.TaskId][]durable.TaskId{}
	for _, record := range s.liveRecordsLocked(staged) {
		if record.Background {
			continue
		}
		for _, each := range s.aboveLocked(parentOf(nodeOf(record)), staged) {
			if each.kind == stepOwnerUnknown {
				break
			}
			if each.kind != stepOwnerTask {
				continue
			}
			owned[each.task] = append(owned[each.task], record.Id)
			if each.node.background {
				break
			}
		}
	}
	return owned
}

// inScopeLocked reports whether ordinary traversal from target reaches start: walking up reaches the scope's
// conversation, or an ownerless one for roots, without crossing a background owner, unless crossBackground. known is
// false while an edge is not loaded.
func (s *TaskScheduler) inScopeLocked(start up, target scope, crossBackground bool) (inScope, known bool) {
	for _, each := range s.aboveLocked(start, nil) {
		switch each.kind {
		case stepOwnerUnknown:
			return false, false
		case stepOwnerConversation:
			if !target.roots && each.conversation == target.conversation {
				return true, true
			}
		case stepOwnerTask:
			if each.node.background && !crossBackground {
				return false, true
			}
		}
	}
	return target.roots, true
}

// belowCancelledLocked reports whether a live owner's cancellation intent reaches start: walking up finds an owner
// with intent before a background owner without it. Terminal owners never cascade (spec §5.4).
func (s *TaskScheduler) belowCancelledLocked(start up) bool {
	for _, each := range s.aboveLocked(start, nil) {
		if each.kind == stepOwnerUnknown {
			return false
		}
		if each.kind != stepOwnerTask {
			continue
		}
		if record, ok := s.live[each.task]; ok && cancellationIntent(record) {
			return true
		}
		if each.node.background {
			return false
		}
	}
	return false
}

// seal is the close listener: it runs synchronously once admission is sealed, before Join.
func (s *TaskScheduler) seal() {
	// Set before taking the mutex: a step holds it across registry reads, and a close that begins inside one must still
	// stop the invocation from dispatching its next phase (scheduler.ts #seal sets #closing synchronously).
	s.sealing.Store(true)
	s.mu.Lock()
	s.closing = true
	unsubscribe := s.unsubscribeRegistry
	var running []*invocation
	for _, invocation := range s.invocations {
		running = append(running, invocation)
	}
	s.mu.Unlock()
	// From here no pass starts, so Join may wait on the background group.
	s.sealOnce.Do(func() { close(s.sealed) })
	unsubscribe()
	s.taskWaiters.RejectAll(closedError())
	s.idleWaiters.RejectAll(closedError())
	s.idleHarness.RejectAll(closedError())
	for _, invocation := range running {
		invocation.cancel(closedError())
	}
}

func (s *TaskScheduler) kick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	if s.draining || !s.enabled || s.closing {
		return
	}
	s.draining = true
	// Never commit synchronously from a commit or registry listener.
	s.background.Go(s.drain)
}

func (s *TaskScheduler) drain() {
	for {
		s.mu.Lock()
		if !s.dirty || !s.enabled || s.closing {
			s.draining = false
			// A wakeup that arrived during a failed pass still needs its pass.
			dirty := s.dirty
			s.mu.Unlock()
			if dirty {
				s.kick()
			}
			return
		}
		s.dirty = false
		s.awaitReconcilesLocked()
		s.mu.Unlock()
		reservations, err := s.reserve()
		if err != nil {
			s.mu.Lock()
			closing := s.closing
			s.draining = false
			dirty := s.dirty
			s.mu.Unlock()
			if !closing {
				s.options.Report(err)
			}
			if dirty {
				s.kick()
			}
			return
		}
		for _, reserved := range reservations {
			s.start(reserved)
		}
	}
}

// reserve reserves every eligible task in one commit and orphans abort-marked tasks no definition can take.
func (s *TaskScheduler) reserve() ([]reservation, error) {
	var reservations []reservation
	_, err := s.options.Session.CommitWith(s.options.Context, func(tx *session.Transaction) (any, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.enabled || s.closing {
			return nil, nil
		}
		// A reconcile that is owed enters the line before any reservation, however the goroutines were scheduled; the
		// drain loop runs this pass again once it is done.
		if s.reconciles > 0 {
			s.dirty = true
			return nil, nil
		}
		if _, err := s.loadScopesLocked(false); err != nil {
			return nil, err
		}
		owned := s.ownedLiveLocked(nil)
		// Taken once per pass, and only when some task is a candidate.
		var snapshot durable.RegistrySnapshot
		for _, record := range s.liveValuesLocked() {
			if _, running := s.invocations[record.Id]; running || len(s.waitingOnLocked(record, owned)) > 0 {
				continue
			}
			if record.State.Status == durable.TaskCompleting {
				continue
			}
			mode := modeRun
			if record.AbortRequested {
				mode = modeAbort
			}
			if snapshot == nil {
				snapshot = s.options.Registry.Snapshot()
			}
			resolution := s.resolveLocked(record, snapshot)
			if resolution.blocked != "" {
				if mode == modeAbort {
					reason := resolution.blocked
					if err := s.terminateLocked(tx, record, anyOutcome{Status: durable.OutcomeOrphaned, Reason: &reason}); err != nil {
						return nil, err
					}
				}
				continue
			}
			if resolution.migrated || record.State.Status != durable.TaskRunning {
				if err := setTask(tx, withState(resolution.record, anyTaskState{Status: durable.TaskRunning, Checkpoint: resolution.record.State.Checkpoint})); err != nil {
					return nil, err
				}
			}
			// Registered on the line, so marks and later reservations see it and close joins it.
			reservations = append(reservations, reservation{invocation: s.createInvocationLocked(record, mode), task: resolution.task, snapshot: snapshot})
		}
		return nil, nil
	}, session.TransactionScope{})
	if err != nil {
		s.mu.Lock()
		for _, reserved := range reservations {
			if s.invocations[reserved.invocation.taskId] == reserved.invocation {
				delete(s.invocations, reserved.invocation.taskId)
			}
			close(reserved.invocation.done)
		}
		s.mu.Unlock()
		return nil, err
	}
	return reservations, nil
}

// waitingOnLocked returns the live tasks a task waits for before its next invocation: its live ordinary owned work when
// abort-marked, since abort runs bottom-up, otherwise the live part of the on of a wait.
func (s *TaskScheduler) waitingOnLocked(record anyTaskRecord, owned map[durable.TaskId][]durable.TaskId) []durable.TaskId {
	if record.AbortRequested {
		return owned[record.Id]
	}
	if record.State.Status != durable.TaskWaiting {
		return nil
	}
	var on []durable.TaskId
	for _, id := range record.State.On {
		if _, live := s.live[id]; live {
			on = append(on, id)
		}
	}
	return on
}

type resolution struct {
	task     durable.AnyTask
	record   anyTaskRecord
	migrated bool
	blocked  string
}

type fit struct {
	task     durable.AnyTask
	migrates bool
	reason   string
	err      error
}

// resolveLocked resolves the record's definition by kind, migrating an older stored version.
func (s *TaskScheduler) resolveLocked(record anyTaskRecord, snapshot durable.RegistrySnapshot) resolution {
	found := s.fitLocked(record, snapshot.Task(record.Kind))
	if found.reason != "" {
		return resolution{blocked: found.reason}
	}
	if !found.migrates {
		return resolution{task: found.task, record: record}
	}
	definition := found.task.AnyDefinition()
	migrated, err := func() (anyTaskRecord, error) {
		if definition.Migrate == nil {
			return anyTaskRecord{}, missingMigration(record, definition)
		}
		var checkpoint durable.JsonValue
		if record.State.Checkpoint != nil {
			checkpoint = *record.State.Checkpoint
		}
		input, next, err := definition.Migrate(record.Input, checkpoint, record.Version)
		if err != nil {
			return anyTaskRecord{}, err
		}
		input, err = durable.CopyJson(input)
		if err != nil {
			return anyTaskRecord{}, err
		}
		next, err = durable.CopyJson(next)
		if err != nil {
			return anyTaskRecord{}, err
		}
		result := record
		result.Version = definition.Version
		result.Input = input
		result.State.Checkpoint = &next
		return result, nil
	}()
	if err != nil {
		s.failedMigrations[record.Id] = failedMigration{task: found.task, err: err}
		s.options.Report(err)
		return resolution{blocked: blockedMigrationFailed}
	}
	return resolution{task: found.task, record: migrated, migrated: true}
}

func (s *TaskScheduler) fitLocked(record anyTaskRecord, task durable.AnyTask) fit {
	if task == nil {
		return fit{reason: blockedMissingTask}
	}
	version := task.AnyDefinition().Version
	if version == record.Version {
		return fit{task: task}
	}
	if version < record.Version {
		return fit{reason: blockedTaskTooOld}
	}
	if failed, ok := s.failedMigrations[record.Id]; ok && sameTaskDefinition(failed.task, task) {
		return fit{reason: blockedMigrationFailed, err: failed.err}
	}
	return fit{task: task, migrates: true}
}

// Inspect returns the scheduling state and every live task with its derived state; read on the Session line. It runs
// no task code: a pending migration shows as ready with Migrates, and only a migration the scheduler already tried, or
// one that cannot exist, shows as failed.
func (s *TaskScheduler) Inspect(snapshot durable.RegistrySnapshot) (string, []TaskInspection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.loadScopesLocked(false); err != nil {
		return "", nil, err
	}
	owned := s.ownedLiveLocked(nil)
	tasks := []TaskInspection{}
	for _, record := range s.liveValuesLocked() {
		tasks = append(tasks, TaskInspection{Record: record, State: s.inspectTaskLocked(record, snapshot, owned)})
	}
	scheduling := "paused"
	if s.closing {
		scheduling = "closing"
	} else if s.enabled {
		scheduling = "running"
	}
	return scheduling, tasks, nil
}

func (s *TaskScheduler) inspectTaskLocked(record anyTaskRecord, snapshot durable.RegistrySnapshot, owned map[durable.TaskId][]durable.TaskId) TaskInspectionState {
	if _, running := s.invocations[record.Id]; running {
		return TaskInspectionState{Kind: TaskInspectionRunning}
	}
	if record.State.Status == durable.TaskCompleting {
		return TaskInspectionState{Kind: TaskInspectionCompleting}
	}
	if on := s.waitingOnLocked(record, owned); len(on) > 0 {
		return TaskInspectionState{Kind: TaskInspectionWaiting, On: on}
	}
	found := s.fitLocked(record, snapshot.Task(record.Kind))
	if found.reason != "" {
		return TaskInspectionState{Kind: TaskInspectionBlocked, Reason: found.reason, Error: found.err}
	}
	if found.migrates && found.task.AnyDefinition().Migrate == nil {
		return TaskInspectionState{Kind: TaskInspectionBlocked, Reason: blockedMigrationFailed, Error: missingMigration(record, found.task.AnyDefinition())}
	}
	return TaskInspectionState{Kind: TaskInspectionReady, Migrates: found.migrates}
}

func (s *TaskScheduler) createInvocationLocked(record anyTaskRecord, mode invocationMode) *invocation {
	ctx, cancel := newLinkedSignal(s.options.Context)
	created := &invocation{
		taskId:         record.Id,
		conversationId: record.ConversationId,
		mode:           mode,
		ctx:            ctx,
		cancel:         cancel,
		watches:        map[durable.WatchHandle[durable.JsonObject]]struct{}{},
		done:           make(chan struct{}),
	}
	s.invocations[record.Id] = created
	return created
}

// start runs a reserved invocation on its own goroutine; Join waits for it through its done channel.
func (s *TaskScheduler) start(reserved reservation) {
	invocation := reserved.invocation
	go func() {
		defer func() {
			s.end(invocation)
			close(invocation.done)
			s.kick()
		}()
		var err error
		if invocation.mode == modeRun {
			err = s.run(reserved)
		} else {
			err = s.runAbort(reserved)
		}
		if err != nil {
			s.options.Report(err)
		}
	}()
}

// run runs phase handlers, each preceded by a step that decides on the line whether the invocation continues.
func (s *TaskScheduler) run(reserved reservation) error {
	invocation := reserved.invocation
	phase := &phaseState{snapshot: reserved.snapshot, task: reserved.task}
	runtime := s.runtime(invocation, phase)
	var previous *phaseResult
	for {
		current, ok := s.step(invocation, func(tx *session.Transaction, current anyTaskRecord) decision {
			return s.decideLocked(tx, current, previous, phase)
		})
		// Close may seal between the decision and dispatch.
		if !ok || s.isSealed() {
			return nil
		}
		var checkpoint durable.JsonValue
		if current.State.Checkpoint != nil {
			checkpoint = *current.State.Checkpoint
		}
		// Each phase handler resolves its agent afresh, at first use.
		phase.mu.Lock()
		phase.agent = nil
		task := phase.task
		phase.mu.Unlock()
		name := phaseName(checkpoint)
		handler := task.AnyDefinition().Phases[name]
		var failure error
		if handler == nil {
			failure = fmt.Errorf("Task %s has no phase %s", current.Kind, name)
		} else {
			failure = runHandler(func() error { return handler(invocation.ctx, current, runtime) })
		}
		previous = &phaseResult{checkpoint: checkpoint, failure: failure, failed: failure != nil}
	}
}

// runHandler runs task code and turns a panic into a fault, as an uncaught throw faults a task upstream.
func runHandler(handler func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return handler()
}

// decideLocked applies the precedence rules for a run invocation, on the line (scheduler.ts:879-912). Rules 1
// (terminal, completing, or waiting) and 2 (closing) are applied by step.
func (s *TaskScheduler) decideLocked(tx *session.Transaction, current anyTaskRecord, previous *phaseResult, phase *phaseState) decision {
	// 3. abort mark: end; a fresh abort invocation starts once the task's ordinary owned work is gone.
	if current.AbortRequested {
		return decision{}
	}
	if previous == nil {
		return decision{proceed: true}
	}
	// 4. uncaught error.
	if previous.failed {
		return decision{fault: previous.failure}
	}
	// 6. no durable progress.
	var checkpoint durable.JsonValue
	if current.State.Checkpoint != nil {
		checkpoint = *current.State.Checkpoint
	}
	if jsonEqual(checkpoint, previous.checkpoint) {
		return decision{fault: fmt.Errorf("Task %s phase %s returned without durable progress", current.Kind, phaseName(previous.checkpoint))}
	}
	// 5. progress: refresh the snapshot; hand over to a replacement definition that can take the task.
	snapshot := s.options.Registry.Snapshot()
	phase.mu.Lock()
	defer phase.mu.Unlock()
	phase.snapshot = snapshot
	next := snapshot.Task(current.Kind)
	if !sameTaskDefinition(next, phase.task) {
		if next != nil && canReserve(next, current) {
			if err := setTask(tx, withState(current, anyTaskState{Status: durable.TaskPending, Checkpoint: current.State.Checkpoint})); err != nil {
				return decision{fault: err}
			}
			return decision{}
		}
		if phase.reported == nil || !sameTaskDefinition(phase.reported.task, next) {
			phase.reported = &reportedTask{task: next}
			cause := "incompatible_task"
			if next == nil {
				cause = "missing_task"
			}
			s.options.Report(&keepsRunningError{taskId: current.Id, kind: current.Kind, cause: cause})
		}
	}
	return decision{proceed: true}
}

// keepsRunningError reports a task that keeps running under its old definition; Cause names why the replacement
// cannot take it.
type keepsRunningError struct {
	taskId durable.TaskId
	kind   string
	cause  string
}

func (err *keepsRunningError) Error() string {
	return fmt.Sprintf("Task %d keeps running under its old %s definition", err.taskId, err.kind)
}

// Cause is missing_task or incompatible_task.
func (err *keepsRunningError) Cause() string { return err.cause }

// runAbort runs the abort handler once; rules 1, 2, and 4 apply, and returning without an outcome faults.
func (s *TaskScheduler) runAbort(reserved reservation) error {
	invocation := reserved.invocation
	s.mu.Lock()
	current, ok := s.live[invocation.taskId]
	closing := s.closing
	s.mu.Unlock()
	if !ok || closing {
		return nil
	}
	phase := &phaseState{snapshot: reserved.snapshot, task: reserved.task}
	runtime := s.runtime(invocation, phase)
	abort := reserved.task.AnyDefinition().Abort
	var failure error
	if abort == nil {
		failure = fmt.Errorf("Task %s has no abort handler", current.Kind)
	} else {
		failure = runHandler(func() error { return abort(invocation.ctx, current, runtime) })
	}
	if failure == nil {
		failure = fmt.Errorf("Abort handler of task %d returned without a terminal outcome", invocation.taskId)
	}
	s.step(invocation, func(*session.Transaction, anyTaskRecord) decision { return decision{fault: failure} })
	return nil
}

// step makes one synchronous decision on the Session line (scheduler.ts:933-960). A task that is no longer running
// (rule 1: terminal, completing, or waiting) or a closing Harness (rule 2) ends the invocation without a write;
// otherwise decide may stage a write and returns whether the invocation continues. Ending happens inside the
// callback, before a fault's Harness cleanup. A rejected step, such as admission after close, also ends the invocation.
func (s *TaskScheduler) step(invocation *invocation, decide func(tx *session.Transaction, current anyTaskRecord) decision) (anyTaskRecord, bool) {
	type stepped struct {
		record  anyTaskRecord
		proceed bool
	}
	result, err := commitOnLine(s.options.Context, s.options.Session, session.TransactionScope{}, func(tx *session.Transaction) (stepped, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		found, ok := s.live[invocation.taskId]
		running := ok && found.State.Status == durable.TaskRunning
		outcome := decision{}
		if running && !s.closing {
			outcome = decide(tx, found)
		}
		if outcome.proceed {
			return stepped{record: found, proceed: true}, nil
		}
		s.endLocked(invocation)
		if outcome.fault != nil {
			if err := s.terminateLocked(tx, found, anyOutcome{Status: durable.OutcomeFaulted, Error: &durable.TaskOutcomeError{Message: outcome.fault.Error()}}); err != nil {
				return stepped{}, err
			}
		}
		return stepped{}, nil
	})
	if err != nil {
		s.end(invocation)
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		if !closing {
			s.options.Report(err)
		}
		return anyTaskRecord{}, false
	}
	return result.record, result.proceed
}

// terminateLocked writes an outcome the scheduler decided. While the task's ordinary owned work is live it holds as
// completing and its Harness cleanup waits for the final commit (spec §5.5, rule 4); otherwise it is terminal with its
// cleanup.
func (s *TaskScheduler) terminateLocked(tx *session.Transaction, record anyTaskRecord, outcome SchedulerOutcome) error {
	if _, err := s.loadScopesLocked(false); err != nil {
		return err
	}
	if _, holds := s.ownedLiveLocked(overlayOf(tx))[record.Id]; holds {
		return setTask(tx, withState(record, anyTaskState{Status: durable.TaskCompleting, Outcome: &outcome}))
	}
	if err := setTask(tx, withState(record, anyTaskState{Status: durable.TaskTerminal, Outcome: &outcome})); err != nil {
		return err
	}
	return s.options.SettleOutcome(tx, record, outcome)
}

// commitStateLocked replaces a running task's state with what it committed. A terminal state holds as completing while
// ordinary owned work is live, judged on the commit's candidates, so work the same commit creates below the task
// counts. A wait is validated first.
func (s *TaskScheduler) commitStateLocked(tx *session.Transaction, invocation *invocation, current anyTaskRecord, next anyTaskState) error {
	if next.Status == durable.TaskWaiting {
		if err := s.validateWaitLocked(tx, invocation, current, next.On, next.Policy); err != nil {
			return err
		}
	}
	if next.Status == durable.TaskTerminal {
		staged := overlayOf(tx)
		if _, err := s.loadScopesLocked(false); err != nil {
			return err
		}
		for _, id := range slices.Sorted(maps.Keys(staged.tasks)) {
			if err := s.loadChainLocked(parentOf(nodeOf(staged.tasks[id])), staged); err != nil {
				return err
			}
		}
		if _, holds := s.ownedLiveLocked(staged)[current.Id]; holds {
			return setTask(tx, withState(current, anyTaskState{Status: durable.TaskCompleting, Outcome: next.Outcome}))
		}
	}
	return setTask(tx, withState(current, next))
}

// validateWaitLocked checks that a wait names existing tasks other than the waiter and its owners, which could never
// finish first, and failFast only tasks the waiter owns. An abort handler cannot wait.
func (s *TaskScheduler) validateWaitLocked(tx *session.Transaction, invocation *invocation, current anyTaskRecord, on []durable.TaskId, policy durable.JoinPolicy) error {
	if invocation.mode == modeAbort {
		return fmt.Errorf("Abort handler of task %d cannot wait", current.Id)
	}
	staged := overlayOf(tx)
	if err := s.loadChainLocked(parentOf(nodeOf(current)), nil); err != nil {
		return err
	}
	owners := map[durable.TaskId]bool{}
	for _, each := range s.aboveLocked(parentOf(nodeOf(current)), nil) {
		if each.kind == stepOwnerTask {
			owners[each.task] = true
		}
	}
	for _, id := range on {
		if id == current.Id || owners[id] {
			return fmt.Errorf("Task %d cannot wait on itself or its owner %d", current.Id, id)
		}
		member, ok := staged.tasks[id]
		if !ok {
			member, ok = s.live[id]
		}
		if !ok {
			stored, err := s.options.Storage.Task(s.options.Context, id)
			if err != nil {
				return err
			}
			if stored == nil {
				return fmt.Errorf("Task %d does not exist", id)
			}
			member = *stored
		}
		if policy == durable.JoinFailFast && (member.Owner == nil || *member.Owner != current.Id) {
			return fmt.Errorf("Task %d can wait failFast only on tasks it owns; %d is not one", current.Id, id)
		}
	}
	return nil
}

func (s *TaskScheduler) end(invocation *invocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endLocked(invocation)
}

// endLocked ends an invocation: its runtime operations fail from now on, its signal is cancelled, its watches stop, and
// its task is free.
func (s *TaskScheduler) endLocked(invocation *invocation) {
	if invocation.ended.Swap(true) {
		return
	}
	if s.invocations[invocation.taskId] == invocation {
		delete(s.invocations, invocation.taskId)
	}
	invocation.watchMu.Lock()
	watches := slices.Collect(maps.Keys(invocation.watches))
	invocation.watchMu.Unlock()
	for _, watch := range watches {
		_, _ = watch.Stop()
	}
	// Pending waits bound to the invocation, such as a tool's WaitForTask, fail with it.
	invocation.cancel(invocation.endedError())
}

// idleLocked reports no live non-background task in the scope; a task whose owner edges are not loaded yet counts as
// inside.
func (s *TaskScheduler) idleLocked(conversationId *durable.ConversationId) bool {
	target := scope{roots: true}
	if conversationId != nil {
		target = scope{conversation: *conversationId}
	}
	for _, record := range s.liveValuesLocked() {
		if record.Background {
			continue
		}
		if inScope, known := s.inScopeLocked(parentOf(nodeOf(record)), target, false); !known || inScope {
			return false
		}
	}
	return true
}

// ─── Invocation runtime ──────────────────────────────────────────────────

func (s *TaskScheduler) runtime(invocation *invocation, phase *phaseState) *taskRuntime {
	return &taskRuntime{scheduler: s, invocation: invocation, phase: phase}
}

// taskRuntime is the erased runtime of one invocation (scheduler.ts:986-1127).
type taskRuntime struct {
	scheduler  *TaskScheduler
	invocation *invocation
	phase      *phaseState
}

var _ durable.ErasedTaskRuntime = (*taskRuntime)(nil)

func (runtime *taskRuntime) check() error {
	if runtime.invocation.ended.Load() {
		return runtime.invocation.endedError()
	}
	return nil
}

func (runtime *taskRuntime) TaskId() durable.TaskId { return runtime.invocation.taskId }

func (runtime *taskRuntime) ConversationId() durable.ConversationId {
	return runtime.invocation.conversationId
}

func (runtime *taskRuntime) Signal() context.Context { return runtime.invocation.ctx }

func (runtime *taskRuntime) Registry() durable.RegistrySnapshot {
	runtime.phase.mu.Lock()
	defer runtime.phase.mu.Unlock()
	return runtime.phase.snapshot
}

func (runtime *taskRuntime) Settings() durable.Settings { return runtime.scheduler.options.Settings() }

func (runtime *taskRuntime) Models() durable.Models { return runtime.scheduler.options.Models }

// resolveAgent resolves the agent at most once per phase handler, at first use, with the invocation's context; it is
// fixed for the phase.
func (runtime *taskRuntime) resolveAgent() *agentResolution {
	phase := runtime.phase
	phase.mu.Lock()
	if phase.agent != nil {
		resolution := phase.agent
		phase.mu.Unlock()
		return resolution
	}
	resolution := &agentResolution{done: make(chan struct{})}
	phase.agent = resolution
	snapshot := phase.snapshot
	phase.mu.Unlock()
	go func() {
		defer close(resolution.done)
		// A resolution that throws (for example a settings getter) fails the waiting callers, as a rejected Promise does.
		resolution.err = runHandler(func() error {
			var err error
			resolution.agent, err = runtime.scheduler.options.Agent(runtime.invocation.ctx, runtime.invocation.conversationId, snapshot)
			return err
		})
	}()
	return resolution
}

func (runtime *taskRuntime) Agent(ctx context.Context) (durable.Agent, error) {
	if err := runtime.check(); err != nil {
		return durable.Agent{}, err
	}
	resolution := runtime.resolveAgent()
	// awaitWithContext (chord context) rejects a caller already cancelled, even when the resolution has settled; a
	// select with both cases ready would choose at random.
	if ctx.Err() != nil {
		return durable.Agent{}, context.Cause(ctx)
	}
	select {
	case <-resolution.done:
		return resolution.agent, resolution.err
	case <-ctx.Done():
		return durable.Agent{}, context.Cause(ctx)
	}
}

func (runtime *taskRuntime) Env(ctx context.Context) (env.ExecutionEnv, error) {
	if err := runtime.check(); err != nil {
		return nil, err
	}
	return runtime.scheduler.options.Env(ctx, runtime.invocation.conversationId)
}

func (runtime *taskRuntime) Hooks() durable.HookRunner[any] { return runtimeHooks{runtime: runtime} }

type runtimeHooks struct{ runtime *taskRuntime }

func (hooks runtimeHooks) Each(name string, invoke func(handlers any) error) error {
	runtime := hooks.runtime
	agent, err := runtime.Agent(runtime.invocation.ctx)
	if err != nil {
		return err
	}
	runtime.phase.mu.Lock()
	taskName := runtime.phase.task.AnyDefinition().Name
	runtime.phase.mu.Unlock()
	for _, handlers := range AgentHooks(agent, taskName) {
		if err := runHandler(func() error { return invoke(handlers) }); err != nil {
			if runtime.invocation.ctx.Err() != nil {
				return err
			}
			runtime.scheduler.options.Report(err)
		}
	}
	return nil
}

func (runtime *taskRuntime) Commit(ctx context.Context, change func(tx durable.Tx, current durable.ErasedRunningTask) (*durable.NextTaskState[durable.JsonValue, durable.JsonValue], error)) error {
	_, err := gated(ctx, runtime, func(tx *session.Transaction, current anyTaskRecord) (struct{}, error) {
		next, err := change(tx, current)
		if err != nil || next == nil {
			return struct{}{}, err
		}
		runtime.scheduler.mu.Lock()
		defer runtime.scheduler.mu.Unlock()
		return struct{}{}, runtime.scheduler.commitStateLocked(tx, runtime.invocation, current, *next)
	})
	return err
}

func (runtime *taskRuntime) Memo(ctx context.Context, name string) (durable.JsonValue, bool, error) {
	if err := runtime.check(); err != nil {
		return nil, false, err
	}
	runtime.scheduler.mu.Lock()
	record, ok := runtime.scheduler.live[runtime.invocation.taskId]
	runtime.scheduler.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	value, found := record.Memos[name]
	return value, found, nil
}

func (runtime *taskRuntime) MemoCandidate(ctx context.Context, name string, candidate durable.JsonValue) (durable.JsonValue, error) {
	return gated(ctx, runtime, func(tx *session.Transaction, current anyTaskRecord) (durable.JsonValue, error) {
		if winner, found := current.Memos[name]; found {
			return winner, nil
		}
		record := current
		record.Memos = maps.Clone(current.Memos)
		if record.Memos == nil {
			record.Memos = map[string]durable.JsonValue{}
		}
		record.Memos[name] = candidate
		return candidate, setTask(tx, record)
	})
}

func (runtime *taskRuntime) GetTask(ctx context.Context, id durable.TaskId) (*anyTaskRecord, error) {
	if err := runtime.check(); err != nil {
		return nil, err
	}
	return readOnLine(runtime.scheduler.options.Session, func() (*anyTaskRecord, error) {
		return runtime.scheduler.options.Storage.Task(ctx, id)
	})
}

func (runtime *taskRuntime) WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error) {
	if err := runtime.check(); err != nil {
		return anyTaskRecord{}, err
	}
	bound, stop := linkSignal(ctx, runtime.invocation.ctx)
	defer stop()
	return runtime.scheduler.WaitForTask(bound, id)
}

func (runtime *taskRuntime) Outcomes(ctx context.Context, ids []durable.TaskId) ([]anyOutcome, error) {
	if err := runtime.check(); err != nil {
		return nil, err
	}
	return readOnLine(runtime.scheduler.options.Session, func() ([]anyOutcome, error) {
		outcomes := make([]anyOutcome, 0, len(ids))
		for _, id := range ids {
			record, err := runtime.scheduler.options.Storage.Task(ctx, id)
			if err != nil {
				return nil, err
			}
			if record == nil || record.State.Status != durable.TaskTerminal || record.State.Outcome == nil {
				return nil, fmt.Errorf("Task %d is not terminal", id)
			}
			outcomes = append(outcomes, *record.State.Outcome)
		}
		return outcomes, nil
	})
}

func (runtime *taskRuntime) Conversation(ctx context.Context, id durable.ConversationId) (durable.ConversationHandle, error) {
	if err := runtime.check(); err != nil {
		return nil, err
	}
	binding := InvocationBinding{Signal: runtime.invocation.ctx, Check: runtime.check}
	return runtime.scheduler.options.Conversation(ctx, id, binding)
}

func (runtime *taskRuntime) Entry(ctx context.Context, id durable.EntryId) (*durable.EntryRecord, error) {
	if err := runtime.check(); err != nil {
		return nil, err
	}
	found, err := readOnLine(runtime.scheduler.options.Session, func() (*durable.EntryAt, error) {
		return runtime.scheduler.options.Storage.VisibleEntry(ctx, runtime.invocation.conversationId, id)
	})
	if err != nil || found == nil {
		return nil, err
	}
	entry := found.Entry
	return &entry, nil
}

func (runtime *taskRuntime) Context(ctx context.Context, conversationId durable.ConversationId, at *durable.EntryId) (durable.ContextView, error) {
	if err := runtime.check(); err != nil {
		return durable.ContextView{}, err
	}
	return ReadContext(ctx, lineAdapter{runtime.scheduler.options.Session}, runtime.scheduler.options.Storage, conversationId, at)
}

// Now returns the Harness clock; after the invocation ended it panics, as upstream's now() throws.
func (runtime *taskRuntime) Now() float64 {
	if err := runtime.check(); err != nil {
		panic(err)
	}
	return runtime.scheduler.options.Now()
}

// Report forwards a non-fatal failure; after the invocation ended it panics, as upstream's report() throws.
func (runtime *taskRuntime) Report(err error) {
	if ended := runtime.check(); ended != nil {
		panic(ended)
	}
	runtime.scheduler.options.Report(err)
}

// Sleep waits until the Harness clock reaches until, rechecking it after every timer.
func (runtime *taskRuntime) Sleep(ctx context.Context, until float64) error {
	if err := runtime.check(); err != nil {
		return err
	}
	signal, stop := linkSignal(ctx, runtime.invocation.ctx)
	defer stop()
	for {
		if signal.Err() != nil {
			return context.Cause(signal)
		}
		remaining := until - runtime.scheduler.options.Now()
		if remaining <= 0 {
			return nil
		}
		timer := time.NewTimer(time.Duration(math.Min(remaining, maxTimerDelay) * float64(time.Millisecond)))
		select {
		case <-timer.C:
		case <-signal.Done():
			timer.Stop()
			return context.Cause(signal)
		}
	}
}

func (runtime *taskRuntime) SnapshotErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.JsonObject, error) {
	if err := runtime.check(); err != nil {
		return nil, err
	}
	return runtime.scheduler.options.Session.SnapshotErased(ctx, token, args...)
}

func (runtime *taskRuntime) SnapshotAsOfErased(ctx context.Context, token durable.AnyDocToken, at durable.EntryId, args ...any) (durable.JsonObject, error) {
	if err := runtime.check(); err != nil {
		return nil, err
	}
	return runtime.scheduler.options.Session.SnapshotAsOfErased(ctx, token, at, args...)
}

func (runtime *taskRuntime) WatchDocErased(ctx context.Context, token durable.AnyDocToken, args ...any) (durable.WatchHandle[durable.JsonObject], error) {
	if err := runtime.check(); err != nil {
		return nil, err
	}
	watch, err := runtime.scheduler.options.Session.WatchDocErased(ctx, token, args...)
	if err != nil || watch == nil {
		return watch, err
	}
	invocation := runtime.invocation
	invocation.watchMu.Lock()
	if invocation.ended.Load() {
		invocation.watchMu.Unlock()
		_, _ = watch.Stop()
		return nil, invocation.endedError()
	}
	invocation.watches[watch] = struct{}{}
	invocation.watchMu.Unlock()
	runtime.scheduler.background.Go(func() {
		<-watch.Closed()
		invocation.watchMu.Lock()
		delete(invocation.watches, watch)
		invocation.watchMu.Unlock()
	})
	return watch, nil
}

// gated commits after rereading the task on the line and gating the invocation.
func gated[T any](ctx context.Context, runtime *taskRuntime, change func(tx *session.Transaction, current anyTaskRecord) (T, error)) (T, error) {
	var zero T
	if err := runtime.check(); err != nil {
		return zero, err
	}
	invocation := runtime.invocation
	scheduler := runtime.scheduler
	conversationId, taskId := invocation.conversationId, invocation.taskId
	return commitOnLine(ctx, scheduler.options.Session, session.TransactionScope{ConversationId: &conversationId, TaskId: &taskId}, func(tx *session.Transaction) (T, error) {
		if err := runtime.check(); err != nil {
			return zero, err
		}
		scheduler.mu.Lock()
		closing := scheduler.closing
		found, ok := scheduler.live[taskId]
		scheduler.mu.Unlock()
		if closing {
			return zero, closedError()
		}
		if !ok {
			return zero, fmt.Errorf("Task %d is terminal", taskId)
		}
		if found.State.Status != durable.TaskRunning {
			return zero, fmt.Errorf("Task %d is %s", taskId, found.State.Status)
		}
		if invocation.mode == modeRun && found.AbortRequested {
			return zero, fmt.Errorf("Task %d has a durable abort mark", taskId)
		}
		return change(tx, found)
	})
}

// linkSignal returns ctx also cancelled by signal; stop releases the link.
func linkSignal(ctx, signal context.Context) (context.Context, func()) {
	return linkedChild(ctx, signal)
}

// cancellationIntent reports a live owner's durable cancellation intent: its abort mark, or a held outcome other than
// completed.
func cancellationIntent(record anyTaskRecord) bool {
	return record.State.Status != durable.TaskTerminal && (record.AbortRequested || failedOutcome(record))
}

// failedOutcome reports whether the record holds or ends with an outcome other than completed.
func failedOutcome(record anyTaskRecord) bool {
	state := record.State
	return (state.Status == durable.TaskCompleting || state.Status == durable.TaskTerminal) && state.Outcome != nil && state.Outcome.Status != durable.OutcomeCompleted
}

func parentOf(node taskNode) up {
	if node.owner != nil {
		return up{isTask: true, task: *node.owner}
	}
	return up{conversation: node.conversationId}
}

func nodeOf(record anyTaskRecord) taskNode {
	return taskNode{conversationId: record.ConversationId, owner: record.Owner, background: record.Background}
}

func ownerTaskOf(record durable.ConversationRecord) *durable.TaskId {
	if record.Owner == nil {
		return nil
	}
	owner := record.Owner.TaskId
	return &owner
}

func overlayOf(tx *session.Transaction) *overlay {
	staged := &overlay{tasks: map[durable.TaskId]anyTaskRecord{}, edges: map[durable.ConversationId]*durable.TaskId{}}
	for _, record := range tx.StagedTasks() {
		staged.tasks[record.Id] = record
	}
	for _, record := range tx.StagedConversations() {
		staged.edges[record.Id] = ownerTaskOf(record)
	}
	return staged
}

func setTask(tx *session.Transaction, record anyTaskRecord) error { return tx.SetTask(record) }

// lineAdapter gives ReadContext the context-taking line reader it declares.
type lineAdapter struct{ session *session.SessionImpl }

func (adapter lineAdapter) ReadOnLine(_ context.Context, job func() (any, error)) (any, error) {
	return adapter.session.ReadOnLine(job)
}

func missingMigration(record anyTaskRecord, definition *durable.AnyTaskDefinition) error {
	return fmt.Errorf("Task %s version %d has no migration from %d", record.Kind, definition.Version, record.Version)
}

// withState replaces a live record's state; memos disappear once an outcome is decided.
func withState(record anyTaskRecord, state anyTaskState) anyTaskRecord {
	record.State = state
	if state.Status == durable.TaskTerminal || state.Status == durable.TaskCompleting {
		record.Memos = nil
	}
	return record
}

// canReserve reports whether a definition can take the task at reservation: same version, or newer with a migration.
func canReserve(task durable.AnyTask, record anyTaskRecord) bool {
	definition := task.AnyDefinition()
	return definition.Version == record.Version || (definition.Version > record.Version && definition.Migrate != nil)
}

// sameTaskDefinition is upstream's identity comparison of registered task definitions.
func sameTaskDefinition(left, right durable.AnyTask) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.AnyDefinition() == right.AnyDefinition()
}

func phaseName(checkpoint durable.JsonValue) string {
	object, _ := checkpoint.(map[string]any)
	name, _ := object["phase"].(string)
	return name
}

// jsonEqual is structural equality of two JSON values; object key order is ignored.
func jsonEqual(left, right durable.JsonValue) bool {
	switch typed := left.(type) {
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(typed) != len(other) {
			return false
		}
		for key, value := range typed {
			counterpart, present := other[key]
			if !present || !jsonEqual(value, counterpart) {
				return false
			}
		}
		return true
	case []any:
		other, ok := right.([]any)
		if !ok || len(typed) != len(other) {
			return false
		}
		for index := range typed {
			if !jsonEqual(typed[index], other[index]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(left, right)
}

var _ = errors.New

// isSealed reports whether the close listener has run; unlike closing it needs no mutex, which a step holds across registry reads.
func (s *TaskScheduler) isSealed() bool { return s.sealing.Load() }
