// Ports packages/durable/src/harness/scheduler.ts.

package harness

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/internal/detach"
	"github.com/MichaelKinsy/PiG/durable/session"
)

type anyTaskRecord = durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
type anyTaskState = durable.TaskState[durable.JsonValue, durable.JsonValue]
type anyOutcome = durable.TaskOutcome[durable.JsonValue]

const schedulerScanPageSize = 256

var liveTaskStatuses = []durable.TaskStatus{durable.TaskPending, durable.TaskRunning, durable.TaskWaiting, durable.TaskSleeping, durable.TaskCompleting}

// Blocked reasons: why a pending task cannot be reserved under a registry snapshot. Derived, never persisted.
const (
	blockedMissingTask     = "missing_task"
	blockedTaskTooOld      = "task_too_old"
	blockedMigrationFailed = "migration_failed"
	blockedRestartLimit    = "restart_limit"
)

type invocationMode int

const (
	modeRun invocationMode = iota
	modeAbort
)

// invocation is one in-memory execution of a task in run or abort mode.
type invocation struct {
	taskId durable.TaskId
	kind   string
	// extension is the extension that installed the definition this invocation runs, as of its reservation; empty for a
	// built-in task.
	extension      string
	conversationId durable.ConversationId
	mode           invocationMode
	// ctx is passed to handlers; cancel ends it with a cause.
	ctx    context.Context
	cancel context.CancelCauseFunc
	// watches acquired through the runtime stop at invocation end.
	watchMu sync.Mutex
	watches map[durable.WatchHandle[durable.JsonObject]]struct{}
	// timers armed through the runtime, by arming order; disarmed at invocation end. Guarded by watchMu.
	timers    map[int]func()
	nextTimer int
	ended     atomic.Bool
	done      chan struct{}
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
	// record is the task as the reservation commit saw it. An abort invocation hands it to the abort handler: upstream's
	// #start runs #runAbort up to its first await in the drain, right after the reservation commit, so a cascade that
	// commits later (a failed owner's request replacing a restart mark) does not reach the handler.
	record anyTaskRecord
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
	// order lists tasks in the order the commit staged them.
	order []durable.TaskId
	edges map[durable.ConversationId]*durable.TaskId
	// links are the links to the staged tasks below does not list, by parent node, for walks down; built at first use.
	links map[treeNode]*orderedSet[treeNode]
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
	// Models returns the host's current model access (HarnessOptions.models); read at each access.
	Models func() durable.Models
	// Agent resolves a conversation's agent against a snapshot; the runtime calls it at most once per phase.
	Agent func(ctx context.Context, conversationId durable.ConversationId, snapshot durable.RegistrySnapshot) (durable.Agent, error)
	// Settings resolves the settings; read at each access.
	Settings func() durable.Settings
	// Env builds a conversation's environment with HarnessOptions.Env.
	Env func(ctx context.Context, conversationId durable.ConversationId) (env.ExecutionEnv, error)
	Now func() float64
	// OnWake is HarnessOptions.OnWake: it replaces the timer for sleeping tasks.
	OnWake func(wake Wake)
	// Timers are the host's timers (HarnessOptions.SetTimer).
	Timers durable.Timers
	// WriteAtClose writes what the callback returns straight to Storage, on the line, after the commits admitted before
	// the seal have settled (BeforeClose of the Session).
	WriteAtClose func(writes func() []durable.StorageWrite) error
	Report       func(err error, info durable.ReportInfo)
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
// live mirrors every committed non-terminal task record: pending, running, waiting, sleeping, and completing. The
// synchronous commit listener updates it on the Session line, so code running on the line reads exactly the committed
// state from it.
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
	// contexts is the context range last read through a task runtime, per conversation: a later read, by any of its
	// tasks, scans only newer entries. It is derived and never persisted; it is dropped at the first idle check after
	// Settings().ContextRetentionMs of idleness, and at close (scheduler.ts #contexts).
	contexts map[durable.ConversationId]*keptContext
	// expiry is the background timer for the earliest expiry of an idle context, unless the host declined it.
	expiry *contextExpiry
	// assistantCalls serves tool tasks the calls of the assistant entries they read, without copying the entry per call.
	assistantCalls assistantCalls

	mu               sync.Mutex
	live             map[durable.TaskId]anyTaskRecord
	liveOrder        *orderedSet[durable.TaskId]
	invocations      map[durable.TaskId]*invocation
	failedMigrations map[durable.TaskId]failedMigration
	edges            map[durable.ConversationId]*durable.TaskId
	settled          map[durable.TaskId]taskNode
	failFastChecks   map[durable.TaskId]bool
	// abandoned holds the live tasks with AbandonOnRestart found at open, from an earlier Harness. The first reservation
	// pass abort-marks them with reason restart, so nothing of them or below them runs a phase again.
	abandoned *orderedSet[durable.TaskId]
	// below is the tree downward around live work: each node's child nodes that are live tasks or have nodes listed below
	// them. A conversation's parent is its owner task; ownerless ones with work are in roots.
	below map[treeNode]*orderedSet[treeNode]
	roots *orderedSet[durable.ConversationId]
	// unloaded holds the known tasks whose chain may not be loaded; every other known task's chain is, and stays, loaded.
	unloaded *orderedSet[durable.TaskId]
	// intent holds the live tasks with cancellation intent, the sources of abort cascades (spec §5.4).
	intent *orderedSet[durable.TaskId]
	// runnable holds the live tasks that are not completing, the candidates of a reservation pass; ones found waiting
	// leave it.
	runnable *orderedSet[durable.TaskId]
	// finalizeChecks holds the completing tasks the next finalize pass checks: those that started holding, and holders of
	// ended work.
	finalizeChecks *orderedSet[durable.TaskId]
	// waiters holds the live waiting tasks by each task in their On.
	waiters map[durable.TaskId]*orderedSet[durable.TaskId]
	// sleeping holds the sleeping live tasks, due or not: the candidates of wakeDueLocked.
	sleeping *orderedSet[durable.TaskId]
	// timer is the timer for the earliest sleep, without OnWake while scheduling runs.
	timer *wakeTimer
	// lastWake is the last value given to OnWake, so an unchanged one is not repeated.
	lastWake *Wake
	// wakeScheduled is set while a recomputation of the wake is queued. wakeMu serializes the recomputations.
	wakeScheduled bool
	wakeMu        sync.Mutex
	// blockedListeners are told when blocked reasons may have changed without a commit.
	blockedListeners map[int]func()
	nextBlocked      int
	// snapshotReported is set once a registry snapshot that panicked for the task graph was reported; it is read per
	// record, so only once.
	snapshotReported bool
	// parkedTasks holds the tasks a pass left out as parked; each pass checks them again, as settings.restartLimit may
	// have risen.
	parkedTasks *orderedSet[durable.TaskId]
	// sealedRunning holds the tasks with an invocation when the close began: the ones Join stops, which an invocation
	// ending on the seal's own rejections must not drop (Pi's join reads them in the microtask that follows the seal).
	sealedRunning map[durable.TaskId]struct{}
	// reconcilingWork counts the reconcile passes underway with work: their commits finalize held outcomes and mark
	// cascades.
	reconcilingWork int
	// dropQueue holds the ended tasks and conversations found unneeded, which sweep drops at the end of the commit's
	// publication unless needed again by then: a chain load installs nodes before the work below them is listed.
	dropQueue          *orderedSet[treeNode]
	sweepScheduled     bool
	reconcileScheduled bool
	// reconciles counts passes scheduled or running; reconcilesIdle is closed when it returns to zero.
	reconciles     int
	reconcilesIdle chan struct{}
	cascadePending bool
	enabled        bool
	// sealing is the closing flag: set by the close listener before anything else, without the mutex (see seal).
	sealing  atomic.Bool
	sealed   chan struct{}
	sealOnce sync.Once
	dirty    bool
	draining bool

	unsubscribeRegistry func()
	taskWaiters         Waiters[durable.TaskId, durable.SettledTask[durable.JsonValue]]
	// idleWaiters waits by conversation; idleHarness waits for the whole Harness.
	idleWaiters Waiters[durable.ConversationId, struct{}]
	idleHarness Waiters[bool, struct{}]
	background  sync.WaitGroup
}

// keptContext is a conversation's kept context range. idleSince is the Harness time it was first seen idle, nil while
// the conversation is busy.
type keptContext struct {
	rng       *contextRange
	idleSince *float64
}

// contextExpiry is the pending timer that settles idle contexts at time at.
type contextExpiry struct {
	at     float64
	disarm func()
}

// wakeTimer is the pending timer that resumes the earliest sleeping task at time at.
type wakeTimer struct {
	at     float64
	disarm func()
}

type failedMigration struct {
	task durable.AnyTask
	err  error
}

// NewTaskScheduler returns a scheduler; Open loads it.
func NewTaskScheduler(options TaskSchedulerOptions) *TaskScheduler {
	return &TaskScheduler{
		options:             options,
		contexts:            map[durable.ConversationId]*keptContext{},
		sealed:              make(chan struct{}),
		live:                map[durable.TaskId]anyTaskRecord{},
		invocations:         map[durable.TaskId]*invocation{},
		failedMigrations:    map[durable.TaskId]failedMigration{},
		edges:               map[durable.ConversationId]*durable.TaskId{},
		settled:             map[durable.TaskId]taskNode{},
		failFastChecks:      map[durable.TaskId]bool{},
		abandoned:           newOrderedSet[durable.TaskId](),
		liveOrder:           newOrderedSet[durable.TaskId](),
		below:               map[treeNode]*orderedSet[treeNode]{},
		roots:               newOrderedSet[durable.ConversationId](),
		unloaded:            newOrderedSet[durable.TaskId](),
		intent:              newOrderedSet[durable.TaskId](),
		runnable:            newOrderedSet[durable.TaskId](),
		finalizeChecks:      newOrderedSet[durable.TaskId](),
		waiters:             map[durable.TaskId]*orderedSet[durable.TaskId]{},
		sleeping:            newOrderedSet[durable.TaskId](),
		parkedTasks:         newOrderedSet[durable.TaskId](),
		blockedListeners:    map[int]func(){},
		dropQueue:           newOrderedSet[treeNode](),
		unsubscribeRegistry: func() {},
	}
}

// setLiveLocked records a live task, keeping first-insertion order like a JavaScript Map.
func (s *TaskScheduler) setLiveLocked(record anyTaskRecord) {
	s.liveOrder.Add(record.Id)
	s.live[record.Id] = record
}

func (s *TaskScheduler) deleteLiveLocked(id durable.TaskId) {
	if _, exists := s.live[id]; !exists {
		return
	}
	delete(s.live, id)
	s.liveOrder.Delete(id)
}

// liveValuesLocked returns the live records in insertion order.
func (s *TaskScheduler) liveValuesLocked() []anyTaskRecord {
	records := make([]anyTaskRecord, 0, s.liveOrder.Len())
	s.liveOrder.Range(func(id durable.TaskId) bool {
		records = append(records, s.live[id])
		return true
	})
	return records
}

// Open loads live tasks and changes surviving running tasks back to pending, counting the restart. It dispatches nothing. A
// task that reaches the restart limit this way is parked and reported.
func (s *TaskScheduler) Open(ctx context.Context) error {
	s.options.Session.ObserveCommits(func(_ context.Context, publication durable.CommitPublication) { s.observe(publication) })
	s.options.Session.SubscribeClose(s.seal)
	s.unsubscribeRegistry = s.options.Registry.Subscribe(func() {
		s.notifyBlocked()
		s.kick()
	})
	limit := s.restartLimit()
	var parked []anyTaskRecord
	_, err := s.options.Session.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
		parked = nil
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
				s.trackLocked(nil, record)
				if record.AbandonOnRestart {
					s.abandoned.Add(record.Id)
				}
				if record.State.Status == durable.TaskRunning {
					restarts := record.Restarts + 1
					pending := withState(record, anyTaskState{Status: durable.TaskPending, Checkpoint: record.State.Checkpoint})
					pending.Restarts = restarts
					if err := setTask(tx, pending); err != nil {
						return nil, err
					}
					if float64(restarts) >= limit && !record.AbortRequested {
						parked = append(parked, record)
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
	for _, record := range parked {
		restarts := record.Restarts + 1
		s.options.Report(fmt.Errorf("Task %d (%s) was cut off %d times without progress; it is parked until retryTask()", record.Id, record.Kind, restarts), s.aboutRecord(durable.ReportTaskParked, record))
	}
	// Derive abort marks a crash left unapplied below cancelled owners, and finalize held outcomes.
	s.mu.Lock()
	s.cascadePending = true
	s.scheduleReconcileLocked()
	s.scheduleWakeLocked()
	s.mu.Unlock()
	return nil
}

// Retry runs a task parked by the restart limit again: it clears its restart count and enables scheduling. It does nothing
// for a task that is not parked, and fails for an unknown one.
func (s *TaskScheduler) Retry(ctx context.Context, id durable.TaskId) error {
	_, err := s.options.Session.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
		current, err := tx.Task(id)
		if err != nil {
			return nil, err
		}
		if current == nil {
			return nil, fmt.Errorf("Task %d does not exist", id)
		}
		status := current.State.Status
		if status == durable.TaskTerminal || status == durable.TaskCompleting {
			return nil, nil
		}
		s.mu.Lock()
		parked := s.parkedLocked(current.AbortRequested, current.Restarts)
		s.mu.Unlock()
		if !parked {
			return nil, nil
		}
		return nil, setTask(tx, progressed(*current))
	}, session.TransactionScope{})
	if err != nil {
		return err
	}
	s.Resume()
	return nil
}

// BlockedSubject is what BlockedReason reads of a task: the task graph keeps only this of each record.
type BlockedSubject struct {
	Id             durable.TaskId
	Kind           string
	Version        int
	AbortRequested bool
	AbortReason    durable.TaskAbortReason
	Restarts       int
	Status         durable.TaskStatus
}

// BlockedSubjectOf is the part of a record BlockedReason reads.
func BlockedSubjectOf(record anyTaskRecord) BlockedSubject {
	return BlockedSubject{Id: record.Id, Kind: record.Kind, Version: record.Version, AbortRequested: record.AbortRequested, AbortReason: record.AbortReason, Restarts: record.Restarts, Status: record.State.Status}
}

// BlockedReason is why a live task cannot run here, if it cannot: its definition (missing_task, task_too_old,
// migration_failed) or the restart limit (restart_limit); empty when it can. A running task, an abort-marked one, and
// one holding its outcome are never blocked.
func (s *TaskScheduler) BlockedReason(record BlockedSubject) durable.BlockedReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := record.Status
	if status == durable.TaskRunning || status == durable.TaskCompleting || status == durable.TaskTerminal {
		return ""
	}
	// An abort request runs its handler or orphans the task; a restart mark waits for the definition, blocked.
	if record.AbortRequested && record.AbortReason == "" {
		return ""
	}
	if s.parkedLocked(record.AbortRequested, record.Restarts) {
		return durable.BlockedRestartLimit
	}
	snapshot := s.snapshotOrReportLocked()
	if snapshot == nil {
		s.snapshotReported = true
		return ""
	}
	found := s.fitLocked(record.Id, record.Version, snapshot.Task(record.Kind))
	if found.reason != "" {
		return durable.BlockedReason(found.reason)
	}
	if found.migrates && found.task.AnyDefinition().Migrate == nil {
		return durable.BlockedMigrationFailed
	}
	return ""
}

// SubscribeBlocked calls listener when blocked reasons may have changed without a commit: a registry change or a failed
// migration. The listener runs off the Session line and the scheduler's lock. The result unsubscribes.
func (s *TaskScheduler) SubscribeBlocked(listener func()) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextBlocked++
	key := s.nextBlocked
	s.blockedListeners[key] = listener
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.blockedListeners, key)
	}
}

// notifyBlocked tells the blocked listeners.
func (s *TaskScheduler) notifyBlocked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notifyBlockedLocked()
}

// notifyBlockedLocked tells the blocked listeners off the lock, in subscription order; one that panics is reported as a
// listener failure.
func (s *TaskScheduler) notifyBlockedLocked() {
	if s.sealing.Load() || len(s.blockedListeners) == 0 {
		return
	}
	listeners := make([]func(), 0, len(s.blockedListeners))
	for _, key := range slices.Sorted(maps.Keys(s.blockedListeners)) {
		listeners = append(listeners, s.blockedListeners[key])
	}
	s.background.Go(func() {
		for _, listener := range listeners {
			session.Contained(listener, func(err error) { s.options.Report(err, durable.ReportInfo{Type: durable.ReportListenerError}) })
		}
	})
}

// Resume enables scheduling, and makes sleeping tasks that are due runnable: a host wakeup, such as an alarm, calls
// this. It is idempotent; the kick does nothing once closing.
func (s *TaskScheduler) Resume() {
	s.mu.Lock()
	s.enabled = true
	s.wakeDueLocked()
	s.mu.Unlock()
	s.kick()
}

// Wake is when the Session next needs a process: Active while some task can run now or is running, At for the earliest
// sleep not yet due. Synchronous, from memory; a task whose chain is not loaded yet counts as it would alone.
func (s *TaskScheduler) Wake() Wake {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wakeLocked()
}

func (s *TaskScheduler) wakeLocked() Wake {
	now := s.options.Now()
	// A reconcile pass with work to do is work too: it finalizes held outcomes and marks cascades, at open as recovery. A
	// process that dies before it lands must be woken again. A pass with nothing to do is not.
	active := len(s.invocations) > 0 || s.reconcilingWork > 0 || s.reconcileWorkLocked()
	var at *float64
	var snapshot durable.RegistrySnapshot
	snapshotFailed := false
	for _, record := range s.liveValuesLocked() {
		state := record.State
		if state.Status == durable.TaskCompleting {
			continue
		}
		if state.Status == durable.TaskWaiting && !record.AbortRequested && slices.ContainsFunc(state.On, func(id durable.TaskId) bool { _, live := s.live[id]; return live }) {
			continue
		}
		if state.Status == durable.TaskSleeping && !record.AbortRequested && state.Until != nil && *state.Until > now {
			if at == nil || *state.Until < *at {
				until := *state.Until
				at = &until
			}
			continue
		}
		if active {
			continue
		}
		if s.parkedLocked(record.AbortRequested, record.Restarts) {
			continue
		}
		// An abort invocation waits for the live work below it, which counts itself.
		if record.AbortRequested && len(s.waitingOnLocked(record, true)) > 0 {
			continue
		}
		// A task no definition can take waits for a registry change, except an abort-marked one, which is orphaned.
		if !record.AbortRequested || record.AbortReason != "" {
			if snapshot == nil && !snapshotFailed {
				snapshot = s.snapshotOrReportLocked()
				snapshotFailed = snapshot == nil
			}
			if snapshot != nil && s.fitLocked(record.Id, record.Version, snapshot.Task(record.Kind)).reason != "" {
				continue
			}
		}
		active = true
	}
	return Wake{Active: active, At: at}
}

// snapshotOrReportLocked takes a registry snapshot; one that panics is reported and yields nil.
func (s *TaskScheduler) snapshotOrReportLocked() (snapshot durable.RegistrySnapshot) {
	defer func() {
		if recovered := recover(); recovered != nil {
			snapshot = nil
			err, ok := recovered.(error)
			if !ok {
				err = fmt.Errorf("%s", durable.ErrorMessage(recovered))
			}
			s.options.Report(err, registryInfo)
		}
	}()
	return s.options.Registry.Snapshot()
}

// Join signals every invocation and waits for them, and for scheduler passes in flight. It writes nothing. Close calls
// it after every close listener has run, so waits, watches, and streams a task holds end with the Session's reason, not
// as cancelled by its signal (scheduler.ts join).
func (s *TaskScheduler) Join() {
	// Session.Close runs the close listeners before BeforeClose (session.ts close), so the seal has normally run. Waiting
	// for it keeps Join from waiting on the background group before seal stops new passes for any other caller.
	<-s.sealed
	s.mu.Lock()
	var done []chan struct{}
	stoppedSet := maps.Clone(s.sealedRunning)
	if stoppedSet == nil {
		stoppedSet = map[durable.TaskId]struct{}{}
	}
	for _, invocation := range s.invocations {
		invocation.cancel(closedError(s.options.Session))
		done = append(done, invocation.done)
		stoppedSet[invocation.taskId] = struct{}{}
	}
	stopped := slices.Sorted(maps.Keys(stoppedSet))
	s.mu.Unlock()
	for _, channel := range done {
		<-channel
	}
	s.background.Wait()
	if len(stopped) == 0 {
		return
	}
	// A close is not a crash: a task it stopped is pending again, so the next open counts no restart for it. Read on the
	// line after commits admitted before the seal, such as an abort mark, so the write keeps them. A failure leaves them
	// running, counted as if cut off; the Storage guard fails the Session and reports it.
	_ = s.options.WriteAtClose(func() []durable.StorageWrite {
		s.mu.Lock()
		defer s.mu.Unlock()
		var writes []durable.StorageWrite
		for _, id := range stopped {
			record, live := s.live[id]
			if !live || record.State.Status != durable.TaskRunning {
				continue
			}
			writes = append(writes, durable.TaskWrite{Value: withState(record, anyTaskState{Status: durable.TaskPending, Checkpoint: record.State.Checkpoint})})
		}
		return writes
	})
}

// Abort commits the abort mark, or settles a task that no registered definition can take as orphaned when nothing it
// owns is live, then joins the run invocation seen on the line; the commit listener signalled it. The abort invocation
// starts once the task's ordinary owned work is gone. A completing task is only marked (scheduler.ts:269-296). A request
// replaces a restart mark, so a task abandoned after a restart that waits for its definition is orphaned; with
// keepRestart, as for an owner's own cleanup, such a task keeps its mark and waits on.
func (s *TaskScheduler) Abort(ctx context.Context, id durable.TaskId, keepRestart bool) (string, error) {
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
		// A restart-marked task has no run invocation; it only waits for its abort.
		if keepRestart && current.AbortReason == durable.AbortedByRestart {
			return marked{result: "marked"}, nil
		}
		if running == nil && current.State.Status != durable.TaskCompleting {
			if _, err := s.loadScopesLocked(false); err != nil {
				return marked{}, err
			}
			if !s.hasOwnedLiveLocked(id, nil, nil) {
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
		if !current.AbortRequested || current.AbortReason != "" {
			if err := setTask(tx, withAbortMark(*current, "")); err != nil {
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
		case <-s.options.Session.Failed():
			// A run that ignores its signal can outlive a failed Session, which ends the wait instead.
			return "", closedError(s.options.Session)
		case <-ctx.Done():
			return "", context.Cause(ctx)
		}
	}
	return result.result, nil
}

// WaitForTask returns the terminal receipt of a task; cancelling ctx cancels only this wait, including its wait for the
// listeners of the batches committed before it to be entered, which it resumes its caller after as upstream's drain
// microtask runs before the code that awaited the promise.
func (s *TaskScheduler) WaitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error) {
	settled, err := s.waitForTask(ctx, id)
	if err != nil {
		return settled, err
	}
	if err := s.options.Session.AwaitDeliveriesEntered(ctx); err != nil {
		return anyTaskRecord{}, err
	}
	return settled, nil
}

func (s *TaskScheduler) waitForTask(ctx context.Context, id durable.TaskId) (durable.SettledTask[durable.JsonValue], error) {
	// Check and register on the line so no terminal publication falls between them.
	type found struct {
		record *anyTaskRecord
		waiter *Waiter[durable.SettledTask[durable.JsonValue]]
	}
	result, err := readOnLine(s.options.Session, func() (found, error) {
		s.mu.Lock()
		closing := s.sealing.Load()
		_, live := s.live[id]
		var waiter *Waiter[durable.SettledTask[durable.JsonValue]]
		if !closing && live {
			waiter = s.taskWaiters.Add(ctx, id)
		}
		s.mu.Unlock()
		if closing {
			return found{}, closedError(s.options.Session)
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
// conversationId is nil, reaches no live non-background task. Like WaitForTask it resumes its caller after the listeners
// of the batches committed before it have been entered, and cancelling ctx cancels that wait too.
func (s *TaskScheduler) WaitForIdle(ctx context.Context, conversationId *durable.ConversationId) error {
	if err := s.waitForIdle(ctx, conversationId); err != nil {
		return err
	}
	return s.options.Session.AwaitDeliveriesEntered(ctx)
}

func (s *TaskScheduler) waitForIdle(ctx context.Context, conversationId *durable.ConversationId) error {
	s.mu.Lock()
	if s.sealing.Load() {
		s.mu.Unlock()
		return closedError(s.options.Session)
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
		var failed error
		s.walkDownLocked(conversationTreeNode(conversationId), walkOptions{crossBackground: background}, func(_ durable.TaskId, node taskNode, live bool, record anyTaskRecord) bool {
			if !live || (node.background && !background) {
				return true
			}
			reached = append(reached, record.Id)
			if !record.AbortRequested || record.AbortReason != "" {
				failed = setTask(tx, withAbortMark(record, ""))
			}
			return failed == nil
		})
		if failed != nil {
			return nil, failed
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
	// Edges first, so the tasks a commit creates in a new conversation hang from it.
	for _, change := range publication.Changes {
		if write, ok := change.(durable.ConversationWrite); ok {
			if _, known := s.edges[write.Value.Id]; !known {
				s.setEdgeLocked(write.Value.Id, ownerTaskOf(write.Value))
			}
		}
	}
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
		var before *anyTaskRecord
		if hadPrevious {
			before = &previous
		}
		s.trackLocked(before, record)
		if record.State.Status == durable.TaskTerminal {
			delete(s.failedMigrations, record.Id)
			delete(s.failFastChecks, record.Id)
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
		// A request replacing a restart mark upgrades the marks of the work below.
		if hadPrevious && previous.AbortReason != "" && record.AbortReason == "" {
			s.cascadePending = true
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
		// Its owners and place never change: what is above it is checked once, when it becomes live.
		if !hadPrevious {
			updated = append(updated, record)
		}
	}
	for _, id := range failed {
		if waiting := s.waiters[id]; waiting != nil {
			for _, waiter := range waiting.Values() {
				if state := s.live[waiter].State; s.live[waiter].Id == waiter && state.Status == durable.TaskWaiting && state.Policy == durable.JoinFailFast {
					s.failFastChecks[waiter] = true
					s.scheduleReconcileLocked()
				}
			}
		}
	}
	for _, change := range publication.Changes {
		// A queued input below a cancelled owner is withdrawn, even after its cascade.
		write, ok := change.(durable.SubmissionWrite)
		if !ok || write.Value.Status != durable.SubmissionQueued || write.Value.Type != durable.SubmissionTypeInput {
			continue
		}
		// Only an owner with cancellation intent withdraws it; a later intent's mark runs the cascade then.
		if s.intent.Len() == 0 {
			continue
		}
		conversation := write.Value.ConversationId
		if !s.knownLocked(conversationTreeNode(conversation)) || s.belowCancelledLocked(up{conversation: conversation}) {
			s.cascadePending = true
		}
	}
	for _, record := range updated {
		// Work created below a cancelled owner, even after its cascade, is aborted too.
		parent := parentOf(nodeOf(record))
		if s.unloaded.Has(record.Id) {
			s.scheduleReconcileLocked()
		} else if !record.Background && !record.AbortRequested && s.belowCancelledLocked(parent) {
			s.cascadePending = true
		}
	}
	// Also retries, with the next commit of any kind, a cascade whose commit failed.
	if s.cascadePending {
		s.scheduleReconcileLocked()
	}
	s.sweepLocked()
	s.mu.Unlock()
	for _, record := range terminal {
		s.options.Session.DeferUntilPublished(func() { s.taskWaiters.Resolve(record.Id, record) })
	}
	if !changed {
		return
	}
	s.options.Session.DeferUntilPublished(s.resolveIdleWaiters)
	s.kick()
}

// resolveIdleWaiters resolves idle waiters, and drops each kept context whose conversation has been idle for the
// retention period (scheduler.ts #settleIdle).
func (s *TaskScheduler) resolveIdleWaiters() {
	now := s.options.Now()
	// #settleIdle and the #scheduleExpiry it calls each read the setting, so a failing source is reported twice.
	retention := s.contextRetentionMs()
	expiryRetention := s.contextRetentionMs()
	s.mu.Lock()
	var idle []durable.ConversationId
	for _, conversationId := range s.idleWaiters.Keys() {
		if s.idleLocked(&conversationId) {
			idle = append(idle, conversationId)
		}
	}
	harness := len(s.idleHarness.Keys()) > 0 && s.idleLocked(nil)
	for conversationId, kept := range s.contexts {
		switch {
		case kept.idleSince != nil && now-*kept.idleSince >= retention:
			delete(s.contexts, conversationId)
		case !s.idleLocked(&conversationId):
			kept.idleSince = nil
		case retention > 0:
			if kept.idleSince == nil {
				kept.idleSince = &now
			}
		default:
			delete(s.contexts, conversationId)
		}
	}
	s.scheduleExpiryLocked(now, expiryRetention)
	s.mu.Unlock()
	for _, conversationId := range idle {
		s.idleWaiters.Resolve(conversationId, struct{}{})
	}
	if harness {
		s.idleHarness.Resolve(true, struct{}{})
	}
}

// contextRetentionMs is Settings().ContextRetentionMs, or 0 when the settings source panics, which it reports
// (scheduler.ts #contextRetentionMs). Kept contexts are only a cache, so dropping them is safe, while a panic here would
// escape commit publication, reconciliation and the expiry timer.
func (s *TaskScheduler) contextRetentionMs() float64 {
	retention, err := s.contextRetention()
	if err != nil {
		s.options.Report(err, settingsInfo)
		return 0
	}
	return retention
}

// contextRetention is Settings().ContextRetentionMs, with a panic of the settings source returned as an error.
func (s *TaskScheduler) contextRetention() (retention float64, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if failure, ok := recovered.(error); ok {
				err = failure
			} else {
				err = fmt.Errorf("%v", recovered)
			}
		}
	}()
	return s.options.Settings().ContextRetentionMs, nil
}

// scheduleExpiryLocked runs resolveIdleWaiters when the earliest idle context expires, on a background timer, which
// never keeps the process alive: close stops it. Where the host declines it, none is kept, and task changes alone check
// expiry.
func (s *TaskScheduler) scheduleExpiryLocked(now float64, retention float64) {
	var at *float64
	for _, kept := range s.contexts {
		if kept.idleSince != nil && (at == nil || *kept.idleSince+retention < *at) {
			at = new(*kept.idleSince + retention)
		}
	}
	if s.expiry != nil && at != nil && s.expiry.at == *at {
		return
	}
	if s.expiry != nil {
		s.expiry.disarm()
		s.expiry = nil
	}
	if at == nil || s.sealing.Load() {
		return
	}
	expiry := &contextExpiry{at: *at}
	disarm := s.options.Timers.Background(func() {
		s.mu.Lock()
		if s.expiry == expiry {
			s.expiry = nil
		}
		closing := s.sealing.Load()
		s.mu.Unlock()
		if !closing {
			s.resolveIdleWaiters()
		}
	}, *at-now)
	if disarm == nil {
		return
	}
	expiry.disarm = disarm
	s.expiry = expiry
}

// ─── Ownership ───────────────────────────────────────────────────────────

func (s *TaskScheduler) scheduleReconcileLocked() {
	if s.reconcileScheduled || s.sealing.Load() {
		return
	}
	s.reconcileScheduled = true
	if s.reconciles == 0 {
		s.reconcilesIdle = make(chan struct{})
	}
	s.reconciles++
	// Taken now, in the turn that scheduled it, as upstream's queued reconcile microtask is.
	ticket := s.options.Session.TakeLineTicket()
	s.background.Go(func() { s.reconcile(ticket) })
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
func (s *TaskScheduler) reconcile(ticket *session.LineTicket) {
	defer s.endReconcile()
	s.mu.Lock()
	s.reconcileScheduled = false
	work := s.reconcileWorkLocked()
	if work {
		s.reconcilingWork++
	}
	cascade := s.cascadePending
	s.cascadePending = false
	checks := slices.Sorted(maps.Keys(s.failFastChecks))
	clear(s.failFastChecks)
	s.mu.Unlock()
	_, err := s.options.Session.CommitWithTicket(ticket, s.options.Context, func(tx *session.Transaction) (any, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.sealing.Load() {
			return nil, nil
		}
		queued, err := s.loadScopesLocked(cascade)
		if err != nil {
			return nil, err
		}
		// A cascade from an abandoned owner passes its restart reason on; any other intent marks, or upgrades a restart
		// mark, as a request. Collected first, so a request wins over a restart mark of the same pass.
		type staged struct {
			record anyTaskRecord
			reason durable.TaskAbortReason
		}
		marks := map[durable.TaskId]staged{}
		var markOrder []durable.TaskId
		mark := func(record anyTaskRecord, reason durable.TaskAbortReason) {
			if found, ok := marks[record.Id]; ok && found.reason == "" {
				return
			}
			if record.AbortRequested && (record.AbortReason == "" || reason != "") {
				return
			}
			if _, ok := marks[record.Id]; !ok {
				markOrder = append(markOrder, record.Id)
			}
			marks[record.Id] = staged{record, reason}
		}
		// Every live foreground task below a live owner with cancellation intent, which is its nearest one: a walk down from
		// each owner with intent does not enter another, whose own walk covers what is below it. Derived again on every
		// pass, as loading edges can reveal a cancelled owner.
		for _, id := range s.intent.Values() {
			owner := s.live[id]
			// Only an owner abandoned after a restart, and nothing else, passes its reason on.
			var reason durable.TaskAbortReason
			if owner.AbortReason == durable.AbortedByRestart && !failedOutcome(owner) {
				reason = durable.AbortedByRestart
			}
			s.walkDownLocked(taskTreeNode(id), walkOptions{enter: func(below durable.TaskId) bool { return !s.intent.Has(below) }}, func(_ durable.TaskId, node taskNode, live bool, record anyTaskRecord) bool {
				if live && !node.background {
					mark(record, reason)
				}
				return true
			})
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
					mark(record, "")
				}
			}
		}
		for _, id := range markOrder {
			if err := setTask(tx, withAbortMark(marks[id].record, marks[id].reason)); err != nil {
				return nil, err
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
		// No extension code runs in this commit: a failure is a storage failure, a host callback, or a bug. None is fixed by
		// running the pass again, so it fails the Session, which reports it.
		s.failSession(err)
	}
	s.mu.Lock()
	if work {
		s.reconcilingWork--
	}
	s.scheduleWakeLocked()
	s.mu.Unlock()
	s.resolveIdleWaiters()
}

// reconcileWorkLocked reports whether a reconcile pass would have work: a cascade to derive, held outcomes to check, or
// failFast waits.
func (s *TaskScheduler) reconcileWorkLocked() bool {
	return s.cascadePending || s.finalizeChecks.Len() > 0 || len(s.failFastChecks) > 0
}

// failSession fails the Session, which reports it, for a failure in one of the scheduler's own commits; ignored once
// closing.
func (s *TaskScheduler) failSession(err error) {
	s.mu.Lock()
	closing := s.sealing.Load()
	s.mu.Unlock()
	if !closing {
		s.options.Session.Fail(err)
	}
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

// finalizeLocked writes the terminal record of every completing task without live ordinary owned work, as a worklist:
// the tasks that started holding, and the holders of work that ended. Finalizing one can free the nearest live task above
// it, which goes on the list. Tasks finalized here have no live work below them, so walks down skip them. In rounds: the
// holders the tasks of one round free are checked once, in the next, so a holder of many tasks that end together is
// walked once, not once per task. A held scheduler outcome gets its Harness cleanup here.
func (s *TaskScheduler) finalizeLocked(tx *session.Transaction) error {
	round := s.finalizeChecks.Values()
	s.finalizeChecks.Clear()
	done := map[durable.TaskId]bool{}
	staged := overlayOf(tx)
	for len(round) > 0 {
		next := newOrderedSet[durable.TaskId]()
		for _, id := range round {
			record, ok := staged.tasks[id]
			if !ok {
				record, ok = s.live[id]
			}
			if !ok || record.State.Status != durable.TaskCompleting || done[id] {
				continue
			}
			if s.hasOwnedLiveLocked(id, staged, done) {
				continue
			}
			outcome := *record.State.Outcome
			terminal := withState(record, anyTaskState{Status: durable.TaskTerminal, Outcome: &outcome})
			if err := setTask(tx, terminal); err != nil {
				return err
			}
			if _, listed := staged.tasks[id]; !listed {
				staged.order = append(staged.order, id)
			}
			staged.tasks[id] = terminal
			done[id] = true
			// Only the scheduler writes faulted and orphaned (spec §5.4); their cleanup waits for this commit.
			if outcome.Status == durable.OutcomeFaulted || outcome.Status == durable.OutcomeOrphaned {
				if err := s.options.SettleOutcome(tx, record, outcome); err != nil {
					return err
				}
			}
			if above, ok := s.nearestLiveAboveLocked(nodeOf(record), staged); ok && above.State.Status == durable.TaskCompleting {
				next.Add(above.Id)
			}
		}
		round = next.Values()
	}
	return nil
}

// loadScopesLocked loads the owner chains of the tasks in unloaded and, with queued, of every conversation with queued
// submissions; it returns the latter. It reads committed Storage directly, so it may run inside a commit callback.
func (s *TaskScheduler) loadScopesLocked(queued bool) ([]durable.ConversationId, error) {
	for _, id := range s.unloaded.Values() {
		// An earlier load in this loop may have passed it.
		if s.unloaded.Has(id) {
			if err := s.loadChainLocked(up{isTask: true, task: id}, nil); err != nil {
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
		if submission.Type != durable.SubmissionTypeInput {
			continue
		}
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

// loadChainLocked loads the owner edges and task nodes from start up to its ownerless root. A known task outside unloaded
// ends the walk, as its chain is loaded; the unloaded tasks passed have a loaded chain afterwards. Loading a node can
// reveal a cancelled owner, so it makes the next reconcile derive cascade marks.
func (s *TaskScheduler) loadChainLocked(start up, staged *overlay) error {
	var passed []durable.TaskId
	at := &start
	for at != nil {
		if at.isTask {
			id := at.task
			node, ok := s.nodeLocked(id, nil)
			// Created in this commit: the load from its own record covers what is above it.
			if _, created := staged.stagedTask(id); !ok && created {
				break
			}
			if ok && !s.unloaded.Has(id) {
				break
			}
			if !ok {
				record, err := s.options.Storage.Task(s.options.Context, id)
				if err != nil {
					return err
				}
				if record == nil {
					return nil
				}
				node = nodeOf(*record)
				if record.State.Status == durable.TaskTerminal {
					s.settleLocked(id, node)
				}
				// A loaded node can reveal a cancelling owner: the next reconcile derives marks again.
				s.scheduleReconcileLocked()
			}
			passed = append(passed, id)
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
			// A loaded node can reveal a cancelling owner: the next reconcile derives marks again.
			s.scheduleReconcileLocked()
		}
		if edge == nil {
			at = nil
		} else {
			at = &up{isTask: true, task: *edge}
		}
	}
	for _, id := range passed {
		s.unloaded.Delete(id)
	}
	return nil
}

// stagedTask returns the commit's candidate for id; staged may be nil.
func (staged *overlay) stagedTask(id durable.TaskId) (anyTaskRecord, bool) {
	if staged == nil {
		return anyTaskRecord{}, false
	}
	record, ok := staged.tasks[id]
	return record, ok
}

func (s *TaskScheduler) setEdgeLocked(conversationId durable.ConversationId, owner *durable.TaskId) {
	s.edges[conversationId] = owner
	// The conversation now hangs from its owner, or is a root; with nothing below it, it waits for the sweep.
	s.relistLocked(conversationTreeNode(conversationId))
	s.dropIfUnneededLocked(conversationTreeNode(conversationId))
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
	// Nothing cancels, the common case: no walk.
	if s.intent.Len() == 0 {
		return false
	}
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

// seal is the close listener: it runs synchronously once admission is sealed, before Join (scheduler.ts #seal).
func (s *TaskScheduler) seal() {
	// The closing flag, set before anything else and without the mutex: a step holds the mutex across registry reads,
	// and a close that begins inside one must still stop the invocation from dispatching its next phase.
	s.sealing.Store(true)
	// The scheduler holds its mutex across the Storage calls of its own passes. When one of them fails the Session, the
	// guard runs the close listeners on that goroutine at once (session.ts fail), so this listener cannot wait for the
	// mutex there. With the flag set, nothing the scheduler does next depends on the rest, which completes as soon as the
	// mutex is free; Join waits for it.
	if !s.mu.TryLock() {
		s.background.Go(func() {
			s.mu.Lock()
			s.finishSealLocked()
		})
		return
	}
	s.finishSealLocked()
}

// finishSealLocked completes the seal; it is called with s.mu held and releases it.
func (s *TaskScheduler) finishSealLocked() {
	s.sealedRunning = make(map[durable.TaskId]struct{}, len(s.invocations))
	for id := range s.invocations {
		s.sealedRunning[id] = struct{}{}
	}
	clear(s.contexts)
	if s.expiry != nil {
		s.expiry.disarm()
		s.expiry = nil
	}
	if s.timer != nil {
		s.timer.disarm()
		s.timer = nil
	}
	unsubscribe := s.unsubscribeRegistry
	s.mu.Unlock()
	// From here no pass starts, so Join may wait on the background group.
	s.sealOnce.Do(func() { close(s.sealed) })
	unsubscribe()
	s.taskWaiters.RejectAll(closedError(s.options.Session))
	s.idleWaiters.RejectAll(closedError(s.options.Session))
	s.idleHarness.RejectAll(closedError(s.options.Session))
}

func (s *TaskScheduler) kick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduleWakeLocked()
	s.dirty = true
	if s.draining || !s.enabled || s.sealing.Load() {
		return
	}
	s.draining = true
	// Never commit synchronously from a commit or registry listener. The pass takes its place on the Session line now,
	// as upstream's queueMicrotask takes it in the publishing turn, so a commit started after this point queues behind.
	ticket := s.options.Session.TakeLineTicket()
	s.background.Go(func() { s.drain(ticket) })
}

func (s *TaskScheduler) drain(ticket *session.LineTicket) {
	defer func() {
		if ticket != nil {
			ticket.Release()
		}
	}()
	for {
		s.mu.Lock()
		if !s.dirty || !s.enabled || s.sealing.Load() {
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
		if ticket == nil {
			s.awaitReconcilesLocked()
		}
		s.mu.Unlock()
		reservations, err := s.reserve(ticket)
		ticket = nil
		if err != nil {
			// As in reconcile: the reservation commit runs no extension code.
			s.failSession(err)
			s.mu.Lock()
			s.draining = false
			dirty := s.dirty
			s.mu.Unlock()
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
func (s *TaskScheduler) reserve(ticket *session.LineTicket) ([]reservation, error) {
	var reservations []reservation
	type faultedTask struct {
		record  anyTaskRecord
		message string
	}
	var faulted []faultedTask
	var abandoned []durable.TaskId
	_, err := s.options.Session.CommitWithTicket(ticket, s.options.Context, func(tx *session.Transaction) (any, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.enabled || s.sealing.Load() {
			return nil, nil
		}
		// A reconcile that is owed enters the line before any reservation, however the goroutines were scheduled; the
		// drain loop runs this pass again once it is done. A pass with a Line ticket has it already: a reconcile
		// scheduled before the pass took its ticket is ahead of it, and one scheduled later is behind it, as upstream's
		// queued jobs are.
		if ticket == nil && s.reconciles > 0 {
			s.dirty = true
			return nil, nil
		}
		for _, id := range s.abandoned.Values() {
			if record, live := s.live[id]; live && !record.AbortRequested {
				if err := setTask(tx, withAbortMark(record, durable.AbortedByRestart)); err != nil {
					return nil, err
				}
			}
		}
		if s.abandoned.Len() > 0 {
			abandoned = s.abandoned.Values()
			return nil, nil
		}
		if _, err := s.loadScopesLocked(false); err != nil {
			return nil, err
		}
		// Every pass picks up sleeps whose time has come: a pass started by any change may be the first to run after a
		// deadline, and the wake it schedules no longer keeps a timer for a due sleep.
		s.wakeDueLocked()
		s.unparkRaisedLocked()
		// Taken once per pass, and only when some task is a candidate.
		var snapshot durable.RegistrySnapshot
		for _, id := range s.runnable.Values() {
			record, live := s.live[id]
			if !live || record.State.Status == durable.TaskCompleting {
				s.runnable.Delete(id)
				continue
			}
			if _, running := s.invocations[id]; running {
				s.runnable.Delete(id)
				continue
			}
			// It returns once what it waits for ends, its record changes, or the work below it ends.
			if len(s.waitingOnLocked(record, true)) > 0 {
				s.runnable.Delete(id)
				continue
			}
			// It returns at the first pass after its time comes, which the timer, Resume, or any change starts.
			if s.notDueLocked(record) {
				s.runnable.Delete(id)
				continue
			}
			// It returns when Retry clears its restart count, or at a pass after settings.restartLimit rose.
			if s.parkedLocked(record.AbortRequested, record.Restarts) {
				s.runnable.Delete(id)
				// Newly parked without a commit, as after a lowered limit: the graph must hear it.
				if !s.parkedTasks.Has(id) {
					s.notifyBlockedLocked()
				}
				s.parkedTasks.Add(id)
				continue
			}
			mode := modeRun
			if record.AbortRequested {
				mode = modeAbort
			}
			// Work below an owner with cancellation intent waits for its cascade mark instead of running a phase; schedule
			// the cascade, which may not have run yet.
			if mode == modeRun && !record.Background && s.belowCancelledLocked(parentOf(nodeOf(record))) {
				s.cascadePending = true
				s.scheduleReconcileLocked()
				continue
			}
			// An abort handler cut off restartLimit times in a row would run forever; nothing is left to park it for.
			if mode == modeAbort && float64(record.Restarts) >= s.restartLimit() {
				message := fmt.Sprintf("Abort handler of task %d (%s) was cut off %d times", id, record.Kind, record.Restarts)
				if err := s.terminateLocked(tx, record, anyOutcome{Status: durable.OutcomeFaulted, Error: &durable.TaskOutcomeError{Message: message, Detail: map[string]any{"reason": durable.ReasonRestartLimit}}}); err != nil {
					return nil, err
				}
				faulted = append(faulted, faultedTask{record: record, message: message})
				continue
			}
			if snapshot == nil {
				snapshot = s.options.Registry.Snapshot()
			}
			resolution := s.resolveLocked(record, snapshot)
			if resolution.blocked != "" {
				// An abandoned task waits for its definition, so its abort handler can clean up.
				if mode == modeAbort && record.AbortReason == "" {
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
			// Registered on the line, so marks and later reservations see it and close joins it; endLocked makes it a
			// candidate again.
			s.runnable.Delete(id)
			reservations = append(reservations, reservation{invocation: s.createInvocationLocked(record, mode, installerOf(snapshot, resolution.task)), task: resolution.task, snapshot: snapshot, record: record})
		}
		return nil, nil
	}, session.TransactionScope{})
	if err != nil {
		s.mu.Lock()
		for _, reserved := range reservations {
			if s.invocations[reserved.invocation.taskId] == reserved.invocation {
				delete(s.invocations, reserved.invocation.taskId)
			}
			s.runnable.Add(reserved.invocation.taskId)
			close(reserved.invocation.done)
		}
		s.mu.Unlock()
		return nil, err
	}
	for _, fault := range faulted {
		info := s.aboutRecord(durable.ReportTaskFaulted, fault.record)
		info.Reason = durable.ReasonRestartLimit
		s.options.Report(errors.New(fault.message), info)
	}
	// Marked, or already marked or gone: done with them. Reserve again, now seeing the marks.
	if len(abandoned) > 0 {
		s.mu.Lock()
		for _, id := range abandoned {
			s.abandoned.Delete(id)
		}
		s.dirty = true
		s.mu.Unlock()
	}
	return reservations, nil
}

// waitingOnLocked returns the live tasks a task waits for before its next invocation: its live ordinary owned work when
// abort-marked, since abort runs bottom-up, otherwise the live part of the on of a wait.
func (s *TaskScheduler) waitingOnLocked(record anyTaskRecord, first bool) []durable.TaskId {
	if record.AbortRequested {
		return s.ownedLiveLocked(record.Id, nil, first, nil)
	}
	if record.State.Status != durable.TaskWaiting {
		return nil
	}
	var on []durable.TaskId
	for _, id := range record.State.On {
		if _, live := s.live[id]; live {
			on = append(on, id)
			if first {
				break
			}
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
	found := s.fitLocked(record.Id, record.Version, snapshot.Task(record.Kind))
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
		// Blocked now without a commit: the graph and the host's wake must hear it.
		s.notifyBlockedLocked()
		s.scheduleWakeLocked()
		info := s.aboutRecord(durable.ReportMigrationFailed, record)
		info.Extension = installerOf(snapshot, found.task)
		s.options.Report(err, info)
		return resolution{blocked: blockedMigrationFailed}
	}
	return resolution{task: found.task, record: migrated, migrated: true}
}

func (s *TaskScheduler) fitLocked(id durable.TaskId, recordVersion int, task durable.AnyTask) fit {
	if task.Definition == nil {
		return fit{reason: blockedMissingTask}
	}
	version := task.AnyDefinition().Version
	if version == recordVersion {
		return fit{task: task}
	}
	if version < recordVersion {
		return fit{reason: blockedTaskTooOld}
	}
	if failed, ok := s.failedMigrations[id]; ok && sameTaskDefinition(failed.task, task) {
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
	tasks := []TaskInspection{}
	for _, record := range s.liveValuesLocked() {
		tasks = append(tasks, TaskInspection{Record: record, State: s.inspectTaskLocked(record, snapshot)})
	}
	scheduling := "paused"
	if s.sealing.Load() {
		scheduling = "closing"
	} else if s.enabled {
		scheduling = "running"
	}
	return scheduling, tasks, nil
}

func (s *TaskScheduler) inspectTaskLocked(record anyTaskRecord, snapshot durable.RegistrySnapshot) TaskInspectionState {
	if _, running := s.invocations[record.Id]; running {
		return TaskInspectionState{Kind: TaskInspectionRunning}
	}
	if record.State.Status == durable.TaskCompleting {
		return TaskInspectionState{Kind: TaskInspectionCompleting}
	}
	if on := s.waitingOnLocked(record, false); len(on) > 0 {
		return TaskInspectionState{Kind: TaskInspectionWaiting, On: on}
	}
	if s.notDueLocked(record) {
		return TaskInspectionState{Kind: TaskInspectionSleeping, Until: *record.State.Until}
	}
	if s.parkedLocked(record.AbortRequested, record.Restarts) {
		return TaskInspectionState{Kind: TaskInspectionBlocked, Reason: blockedRestartLimit}
	}
	found := s.fitLocked(record.Id, record.Version, snapshot.Task(record.Kind))
	if found.reason != "" {
		return TaskInspectionState{Kind: TaskInspectionBlocked, Reason: found.reason, Error: found.err}
	}
	if found.migrates && found.task.AnyDefinition().Migrate == nil {
		return TaskInspectionState{Kind: TaskInspectionBlocked, Reason: blockedMigrationFailed, Error: missingMigration(record, found.task.AnyDefinition())}
	}
	return TaskInspectionState{Kind: TaskInspectionReady, Migrates: found.migrates}
}

func (s *TaskScheduler) createInvocationLocked(record anyTaskRecord, mode invocationMode, extension string) *invocation {
	ctx, cancel := newLinkedSignal(s.options.Context)
	created := &invocation{
		taskId:         record.Id,
		kind:           record.Kind,
		extension:      extension,
		conversationId: record.ConversationId,
		mode:           mode,
		ctx:            ctx,
		cancel:         cancel,
		watches:        map[durable.WatchHandle[durable.JsonObject]]struct{}{},
		timers:         map[int]func(){},
		done:           make(chan struct{}),
	}
	s.invocations[record.Id] = created
	return created
}

// beforeInvocation, when set by a test, runs between the reservation commit and the invocation's start: it lets a test
// commit what would otherwise race with the start.
var beforeInvocation func(s *TaskScheduler, invocation *invocation)

// start runs a reserved invocation on its own goroutine; Join waits for it through its done channel.
func (s *TaskScheduler) start(reserved reservation) {
	invocation := reserved.invocation
	if beforeInvocation != nil {
		beforeInvocation(s, invocation)
	}
	// pig divergence (D181): an invocation runs on its own goroutine, so the commits of a job nothing awaits (a conversation-owned
	// task its creator does not wait for) reach the Session line in an order set by goroutine scheduling; Pi's microtask hops
	// fix that order against the generation chain. The final state is the same; commit sequence numbers can differ.
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
			info := s.aboutInvocation(durable.ReportTaskError, invocation)
			s.options.Report(err, info)
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
			return s.decideLocked(tx, current, previous, phase, invocation.extension)
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

// runHandler runs task code and turns a panic into a fault, as an uncaught throw faults a task upstream; the fault's
// message is errorMessage of the thrown value (scheduler.ts:1420).
func runHandler(handler func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.New(durable.ErrorMessage(recovered))
		}
	}()
	return handler()
}

// decideLocked applies the precedence rules for a run invocation, on the line (scheduler.ts:879-912). Rules 1
// (terminal, completing, or waiting) and 2 (closing) are applied by step.
func (s *TaskScheduler) decideLocked(tx *session.Transaction, current anyTaskRecord, previous *phaseResult, phase *phaseState, extension string) decision {
	// 3. abort mark: end; a fresh abort invocation starts once the task's ordinary owned work is gone.
	if current.AbortRequested {
		return decision{}
	}
	// An owner's cancellation intent ends it too, before its cascade marks it; reservation then holds it back.
	if !current.Background && s.belowCancelledLocked(parentOf(nodeOf(current))) {
		s.cascadePending = true
		s.scheduleReconcileLocked()
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
		if next.Definition != nil && canReserve(next, current) {
			if err := setTask(tx, withState(current, anyTaskState{Status: durable.TaskPending, Checkpoint: current.State.Checkpoint})); err != nil {
				return decision{fault: err}
			}
			return decision{}
		}
		if phase.reported == nil || !sameTaskDefinition(phase.reported.task, next) {
			phase.reported = &reportedTask{task: next}
			reason := durable.ReasonIncompatibleTask
			if next.Definition == nil {
				reason = durable.ReasonMissingTask
			}
			info := s.aboutRecord(durable.ReportTaskOutdated, current)
			info.Extension, info.Reason = extension, reason
			s.options.Report(fmt.Errorf("Task %d keeps running under its old %s definition", current.Id, current.Kind), info)
		}
	}
	return decision{proceed: true}
}

// runAbort runs the abort handler once; rules 1, 2, and 4 apply, and returning without an outcome faults.
func (s *TaskScheduler) runAbort(reserved reservation) error {
	invocation := reserved.invocation
	s.mu.Lock()
	_, live := s.live[invocation.taskId]
	closing := s.sealing.Load()
	s.mu.Unlock()
	if !live || closing {
		return nil
	}
	current := reserved.record
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
	var fault error
	result, err := commitOnLine(s.options.Context, s.options.Session, session.TransactionScope{}, func(tx *session.Transaction) (stepped, error) {
		fault = nil
		s.mu.Lock()
		defer s.mu.Unlock()
		found, ok := s.live[invocation.taskId]
		running := ok && found.State.Status == durable.TaskRunning
		outcome := decision{}
		if running && !s.sealing.Load() {
			outcome = decide(tx, found)
		}
		if outcome.proceed {
			return stepped{record: found, proceed: true}, nil
		}
		s.endLocked(invocation)
		if outcome.fault != nil {
			fault = outcome.fault
			if err := s.terminateLocked(tx, found, anyOutcome{Status: durable.OutcomeFaulted, Error: &durable.TaskOutcomeError{Message: outcome.fault.Error()}}); err != nil {
				return stepped{}, err
			}
		}
		return stepped{}, nil
	})
	if err != nil {
		// The step writes the scheduler's own decision, a fault or a terminal record; a failure there would otherwise leave
		// the task running, to be reserved and run again.
		s.end(invocation)
		s.failSession(err)
		return anyTaskRecord{}, false
	}
	// Reported once the fault is durable: a host may count it, for example toward a breaker.
	if fault != nil {
		info := s.aboutInvocation(durable.ReportTaskFaulted, invocation)
		info.Reason = durable.ReasonThrew
		s.options.Report(fault, info)
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
	if s.hasOwnedLiveLocked(record.Id, overlayOf(tx), nil) {
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
	if next.Status == durable.TaskSleeping {
		if err := validateSleep(invocation, current.Id, next.Until); err != nil {
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
		if s.hasOwnedLiveLocked(current.Id, staged, nil) {
			return setTask(tx, withState(progressed(current), anyTaskState{Status: durable.TaskCompleting, Outcome: next.Outcome}))
		}
	}
	// A state the task committed that differs from its current one is progress: its restart count starts over. The same
	// checkpoint committed again, for example as intent before an effect, is not.
	if sameState(current.State, next) {
		return setTask(tx, withState(current, next))
	}
	return setTask(tx, withState(progressed(current), next))
}

// validateWaitLocked checks that a wait names existing tasks, and failFast only tasks the waiter owns. It may not close
// a cycle in the wait-for graph, whose edges run from each task to what it waits on and to its live ordinary owned work
// (spec §5.5); waiting on itself or on an owner holding it are the shortest such cycles. An abort handler cannot wait.
//
// Invariant: the wait-for graph is acyclic. Only a wait adds an edge between existing tasks, as a new task has only
// incoming edges, so checking each wait maintains it.
func (s *TaskScheduler) validateWaitLocked(tx *session.Transaction, invocation *invocation, current anyTaskRecord, on []durable.TaskId, policy durable.JoinPolicy) error {
	if invocation.mode == modeAbort {
		return fmt.Errorf("Abort handler of task %d cannot wait", current.Id)
	}
	staged := overlayOf(tx)
	var targets []durable.TaskId
	for _, id := range on {
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
		ownedByCurrent := member.Owner != nil && *member.Owner == current.Id
		if policy == durable.JoinFailFast && !ownedByCurrent {
			return fmt.Errorf("Task %d can wait failFast only on tasks it owns; %d is not one", current.Id, id)
		}
		// A live child already holds the waiter (task-owned tasks are never background): waiting on it adds no edge. A
		// terminal task has no edges.
		if member.State.Status != durable.TaskTerminal && !ownedByCurrent {
			targets = append(targets, id)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	cycle, err := s.waitCycleLocked(current.Id, targets, staged)
	if err != nil {
		return err
	}
	if cycle != nil {
		steps := make([]string, 0, len(cycle)+1)
		steps = append(steps, strconv.FormatInt(int64(current.Id), 10))
		for _, id := range cycle {
			steps = append(steps, strconv.FormatInt(int64(id), 10))
		}
		return fmt.Errorf("Task %d cannot wait on %d: %s is a wait cycle", current.Id, cycle[0], strings.Join(steps, " -> "))
	}
	return nil
}

// waitCycleLocked returns the path from a member of on back to task in the wait-for graph, if the wait would close a
// cycle (spec §5.1). An edge X -> Y means X cannot settle before Y: Y is in the on of waiting X that is not
// abort-marked, or Y is live ordinary owned work of X, which holds X as completing. Each task is visited once and each
// subtree walked once, so a check is bounded by the live work it reaches.
func (s *TaskScheduler) waitCycleLocked(task durable.TaskId, on []durable.TaskId, staged *overlay) ([]durable.TaskId, error) {
	// Owned work is found by walks down, which need the chains of every live task loaded.
	if _, err := s.loadScopesLocked(false); err != nil {
		return nil, err
	}
	// from maps each reached task to the task whose edge reached it; the members of on map to the zero value with ok false.
	type via struct {
		task durable.TaskId
		ok   bool
	}
	from := map[durable.TaskId]via{}
	walked := map[durable.TaskId]bool{}
	var pending []durable.TaskId
	reach := func(id durable.TaskId, previous via) {
		if _, seen := from[id]; seen {
			return
		}
		from[id] = previous
		pending = append(pending, id)
	}
	path := func(end durable.TaskId) []durable.TaskId {
		steps := []durable.TaskId{end}
		for at := from[end]; at.ok; at = from[at.task] {
			steps = append(steps, at.task)
		}
		slices.Reverse(steps)
		return steps
	}
	for _, id := range on {
		reach(id, via{})
	}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if id == task {
			return path(id), nil
		}
		record, ok := staged.tasks[id]
		if !ok {
			record, ok = s.live[id]
		}
		if !ok || record.State.Status == durable.TaskTerminal {
			continue
		}
		// An abort-marked task waits only for its owned work, as abort runs bottom-up (waitingOnLocked).
		if record.State.Status == durable.TaskWaiting && !record.AbortRequested {
			for _, member := range record.State.On {
				reach(member, via{task: id, ok: true})
			}
		}
		if walked[id] {
			continue
		}
		walked[id] = true
		// Below a walked task the walk does not go again: its own walk covered what lies below.
		var found []durable.TaskId
		options := walkOptions{staged: staged, enter: func(below durable.TaskId) bool { return !walked[below] }}
		s.walkDownLocked(taskTreeNode(id), options, func(below durable.TaskId, node taskNode, live bool, _ anyTaskRecord) bool {
			if !live || node.background {
				return true
			}
			found = append(found, below)
			reach(below, via{task: id, ok: true})
			return true
		})
		for _, below := range found {
			walked[below] = true
		}
	}
	return nil, nil
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
		if _, live := s.live[invocation.taskId]; live {
			s.runnable.Add(invocation.taskId)
		}
	}
	invocation.watchMu.Lock()
	watches := slices.Collect(maps.Keys(invocation.watches))
	invocation.watchMu.Unlock()
	for _, watch := range watches {
		_, _ = watch.Stop()
	}
	invocation.watchMu.Lock()
	timers := slices.Collect(maps.Values(invocation.timers))
	clear(invocation.timers)
	invocation.watchMu.Unlock()
	for _, disarm := range timers {
		disarm()
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
	// Tasks whose chain is not loaded are not listed below their scope yet; one counts as inside unless its walk up decides
	// otherwise.
	idle := true
	s.unloaded.Range(func(id durable.TaskId) bool {
		record, live := s.live[id]
		if live && !record.Background {
			if inScope, known := s.inScopeLocked(parentOf(nodeOf(record)), target, false); !known || inScope {
				idle = false
			}
		}
		return idle
	})
	if !idle {
		return false
	}
	starts := s.roots.Values()
	if conversationId != nil {
		starts = []durable.ConversationId{*conversationId}
	}
	for _, start := range starts {
		s.walkDownLocked(conversationTreeNode(start), walkOptions{}, func(_ durable.TaskId, node taskNode, live bool, _ anyTaskRecord) bool {
			if live && !node.background {
				idle = false
			}
			return idle
		})
		if !idle {
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

func (runtime *taskRuntime) Models() durable.Models { return runtime.scheduler.options.Models() }

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
	// scheduler.ts:1132-1137 agent() (no caller context): rejects only once the invocation ended, not when it is signalled, so the handlers of a
	// signalled invocation still run and their error propagates (scheduler.ts:1141).
	if err := runtime.check(); err != nil {
		return err
	}
	resolution := runtime.resolveAgent()
	<-resolution.done
	agent, err := resolution.agent, resolution.err
	if err != nil {
		return err
	}
	runtime.phase.mu.Lock()
	taskName := runtime.phase.task.AnyDefinition().Name
	runtime.phase.mu.Unlock()
	for _, hook := range AgentHooks(agent, taskName) {
		if err := runHandler(func() error { return invoke(hook.Handlers) }); err != nil {
			if runtime.invocation.ctx.Err() != nil {
				return err
			}
			runtime.scheduler.options.Report(err, durable.ReportInfo{Type: durable.ReportExtensionError, Extension: hook.Extension, Task: runtime.invocation.reportTask()})
		}
	}
	return nil
}

// lineHold keeps the Session line position right behind a task's terminal commit for what the invocation's code does once
// the commit returned. Upstream's code after the commit's await runs from the microtask queue, ahead of the scheduler pass
// that the commit's publication queued, so what it settles runs before the work the commit made runnable (a joining owner's
// next step). Here that work takes the line behind the position the commit's own step takes: kick takes the pass's ticket in
// the publication, and an owner's steps are queued after its reservation. Holding the position until the code ran keeps the
// upstream order.
type lineHold struct {
	mu       sync.Mutex
	ticket   *session.LineTicket
	released bool
}

type lineHoldKey struct{}

// withLineHold returns ctx carrying a hold that the next terminal commit made with it takes; release gives it up.
func withLineHold(ctx context.Context) (context.Context, *lineHold) {
	hold := &lineHold{}
	return context.WithValue(ctx, lineHoldKey{}, hold), hold
}

// take takes the position on line, on the line itself so it follows the commit's job and precedes the pass the commit queues. A
// Session that began closing gives none.
func (hold *lineHold) take(line *session.SessionImpl) {
	hold.mu.Lock()
	defer hold.mu.Unlock()
	if hold.ticket == nil && !hold.released {
		hold.ticket = line.TakeLineTicket()
	}
}

// release gives the position up; it does nothing when none was taken.
func (hold *lineHold) release() {
	hold.mu.Lock()
	ticket := hold.ticket
	hold.ticket, hold.released = nil, true
	hold.mu.Unlock()
	if ticket != nil {
		ticket.Release()
	}
}

func (runtime *taskRuntime) Commit(ctx context.Context, change func(tx durable.Tx, current durable.ErasedRunningTask) (*durable.NextTaskState[durable.JsonValue, durable.JsonValue], error)) error {
	_, err := gated(ctx, runtime, func(tx *session.Transaction, current anyTaskRecord) (struct{}, error) {
		next, err := change(tx, current)
		if err != nil || next == nil {
			return struct{}{}, err
		}
		if next.Status == durable.TaskTerminal {
			if hold, ok := ctx.Value(lineHoldKey{}).(*lineHold); ok {
				hold.take(runtime.scheduler.options.Session)
			}
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

// AbortOwned aborts a task this task owns and returns once it is terminal (scheduler.ts abortOwned). The abort request and
// the wait end with the invocation.
func (runtime *taskRuntime) AbortOwned(ctx context.Context, id durable.TaskId) error {
	if err := runtime.check(); err != nil {
		return err
	}
	bound, stop := linkSignal(ctx, runtime.invocation.ctx)
	defer stop()
	record, err := readOnLine(runtime.scheduler.options.Session, func() (*anyTaskRecord, error) {
		return runtime.scheduler.options.Storage.Task(bound, id)
	})
	if err != nil {
		return err
	}
	if record == nil || record.Owner == nil || *record.Owner != runtime.invocation.taskId {
		return fmt.Errorf("Task %d is not owned by task %d", id, runtime.invocation.taskId)
	}
	if record.State.Status == durable.TaskTerminal {
		return nil
	}
	if _, err := runtime.scheduler.Abort(bound, id, true); err != nil {
		return err
	}
	_, err = runtime.scheduler.WaitForTask(bound, id)
	return err
}

// OwnedTasks lists the live tasks this task owns directly, in ID order, whichever invocation created them (scheduler.ts
// ownedTasks): every live task is listed under its parent node, its owner task when it has one.
func (runtime *taskRuntime) OwnedTasks(ctx context.Context) ([]anyTaskRecord, error) {
	if err := runtime.check(); err != nil {
		return nil, err
	}
	bound, stop := linkSignal(ctx, runtime.invocation.ctx)
	defer stop()
	return readOnLine(runtime.scheduler.options.Session, func() ([]anyTaskRecord, error) {
		// The step that ends the invocation may have run on the line before this.
		if err := runtime.check(); err != nil {
			return nil, err
		}
		if err := bound.Err(); err != nil {
			return nil, context.Cause(bound)
		}
		s := runtime.scheduler
		s.mu.Lock()
		var owned []anyTaskRecord
		if children := s.below[taskTreeNode(runtime.invocation.taskId)]; children != nil {
			children.Range(func(node treeNode) bool {
				if node.conversation {
					return true
				}
				if record, live := s.live[durable.TaskId(node.id)]; live && record.Owner != nil && *record.Owner == runtime.invocation.taskId {
					owned = append(owned, record)
				}
				return true
			})
		}
		s.mu.Unlock()
		slices.SortFunc(owned, func(a, b anyTaskRecord) int { return cmp.Compare(a.Id, b.Id) })
		copies := make([]anyTaskRecord, 0, len(owned))
		for _, record := range owned {
			encoded, err := json.Marshal(record)
			if err != nil {
				return nil, err
			}
			var copied anyTaskRecord
			if err := json.Unmarshal(encoded, &copied); err != nil { //perf:ok once per owned task per OwnedTasks call (a script asking for its tasks); the copy must not alias scheduler state
				return nil, err
			}
			copies = append(copies, copied)
		}
		return copies, nil
	})
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

func (runtime *taskRuntime) Context(ctx context.Context, conversationId durable.ConversationId, options *durable.ContextOptions) (durable.ContextView, error) {
	var at *durable.EntryId
	if options != nil {
		at = options.At
	}
	if err := runtime.check(); err != nil {
		return durable.ContextView{}, err
	}
	scheduler := runtime.scheduler
	scheduler.mu.Lock()
	var previous *contextRange
	if kept := scheduler.contexts[conversationId]; kept != nil {
		previous = kept.rng
	}
	scheduler.mu.Unlock()
	view, rng, err := readContextFrom(ctx, lineAdapter{scheduler.options.Session}, scheduler.options.Storage, conversationId, at, previous)
	if err != nil {
		return durable.ContextView{}, err
	}
	scheduler.keepContext(runtime, conversationId, rng)
	if ctx.Value(sharedContextKey{}) != nil {
		return view, nil
	}
	return detach.ContextView(view), nil
}

// keepContext keeps rng unless the invocation ended or a concurrent read already kept a newer range. A read of
// another, idle conversation starts or continues its retention period (scheduler.ts runtime.context).
func (s *TaskScheduler) keepContext(runtime *taskRuntime, conversationId durable.ConversationId, rng *contextRange) {
	if rng == nil {
		return
	}
	now := s.options.Now()
	// Read outside the lock, which the settings source must not run under; Pi reads it only for an idle conversation, so
	// only that branch reports a failure, and a failure keeps nothing, as a retention of 0.
	retention, retentionErr := s.contextRetention()
	s.mu.Lock()
	kept := s.contexts[conversationId]
	if s.sealing.Load() || runtime.invocation.ended.Load() || (kept != nil && kept.rng.bounds.Tail > rng.bounds.Tail) {
		s.mu.Unlock()
		return
	}
	idle := s.idleLocked(&conversationId)
	switch {
	case !idle:
		s.contexts[conversationId] = &keptContext{rng: rng}
	case retentionErr == nil && retention > 0:
		idleSince := now
		if kept != nil && kept.idleSince != nil {
			idleSince = *kept.idleSince
		}
		s.contexts[conversationId] = &keptContext{rng: rng, idleSince: &idleSince}
		s.scheduleExpiryLocked(now, retention)
	}
	s.mu.Unlock()
	if idle && retentionErr != nil {
		s.options.Report(retentionErr, settingsInfo)
	}
}

// Now returns the Harness clock; after the invocation ended it panics, as upstream's now() throws.
func (runtime *taskRuntime) Now() float64 {
	if err := runtime.check(); err != nil {
		panic(err)
	}
	return runtime.scheduler.options.Now()
}

// Report forwards a non-fatal failure; after the invocation ended it panics, as upstream's report() throws. The task's own
// failure names the task's extension; any other names whose code failed, if anyone's.
func (runtime *taskRuntime) Report(err error, from ...durable.ReportFrom) {
	if ended := runtime.check(); ended != nil {
		panic(ended)
	}
	invocation := runtime.invocation
	if len(from) == 0 {
		runtime.scheduler.options.Report(err, runtime.scheduler.aboutInvocation(durable.ReportTaskError, invocation))
		return
	}
	switch from[0].Type {
	case durable.ReportFromExtension:
		runtime.scheduler.options.Report(err, durable.ReportInfo{Type: durable.ReportExtensionError, Task: invocation.reportTask(), Extension: from[0].Extension})
	default:
		runtime.scheduler.options.Report(err, durable.ReportInfo{Type: durable.ReportHostError, Callback: from[0].Callback, Task: invocation.reportTask()})
	}
}

// SleepUntil commits a sleep: the task holds no invocation until the Harness clock reaches until, then resumes at
// checkpoint. It resolves once the sleep is stored, not when the time comes (scheduler.ts sleepUntil).
func (runtime *taskRuntime) SleepUntil(ctx context.Context, until float64, checkpoint durable.JsonValue) error {
	_, err := gated(ctx, runtime, func(tx *session.Transaction, current anyTaskRecord) (struct{}, error) {
		runtime.scheduler.mu.Lock()
		defer runtime.scheduler.mu.Unlock()
		return struct{}{}, runtime.scheduler.commitStateLocked(tx, runtime.invocation, current, anyTaskState{Status: durable.TaskSleeping, Checkpoint: &checkpoint, Until: &until})
	})
	return err
}

// SetTimer calls callback once after about delayMs on the Harness's timer. An ended invocation's timers never fire, and
// the invocation's end disarms them (scheduler.ts setTimer).
func (runtime *taskRuntime) SetTimer(callback func(), delayMs float64) func() {
	invocation := runtime.invocation
	if invocation.ended.Load() {
		return func() {}
	}
	invocation.watchMu.Lock()
	invocation.nextTimer++
	key := invocation.nextTimer
	invocation.watchMu.Unlock()
	fired := false
	disarm := runtime.scheduler.options.Timers.Foreground(func() {
		invocation.watchMu.Lock()
		fired = true
		delete(invocation.timers, key)
		invocation.watchMu.Unlock()
		if !invocation.ended.Load() {
			callback()
		}
	}, delayMs)
	// The end may clear the timers, or the timer may fire, between the check above and here: store the disarm only while
	// neither happened, as WatchDocErased does, so no host timer outlives the invocation and no fired timer stays listed.
	invocation.watchMu.Lock()
	if invocation.ended.Load() {
		invocation.watchMu.Unlock()
		disarm()
		return func() {}
	}
	if !fired {
		invocation.timers[key] = disarm
	}
	invocation.watchMu.Unlock()
	return func() {
		invocation.watchMu.Lock()
		delete(invocation.timers, key)
		invocation.watchMu.Unlock()
		disarm()
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
		closing := scheduler.sealing.Load()
		found, ok := scheduler.live[taskId]
		scheduler.mu.Unlock()
		if closing {
			return zero, closedError(scheduler.options.Session)
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
		if _, seen := staged.tasks[record.Id]; !seen {
			staged.order = append(staged.order, record.Id)
		}
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

// withAbortMark is the record with an abort mark: restart for an abandonment; without a reason, a request, which
// replaces restart.
// The restart count starts over: it now counts cut-offs of the abort handler.
func withAbortMark(record anyTaskRecord, reason durable.TaskAbortReason) anyTaskRecord {
	record.AbortRequested = true
	record.AbortReason = reason
	record.Restarts = 0
	return record
}

// withState replaces a live record's state; memos and the restart count disappear once an outcome is decided.
func withState(record anyTaskRecord, state anyTaskState) anyTaskRecord {
	record.State = state
	if state.Status == durable.TaskTerminal || state.Status == durable.TaskCompleting {
		record.Memos = nil
		record.Restarts = 0
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
	return left.Definition == right.Definition
}

func phaseName(checkpoint durable.JsonValue) string {
	name, _ := jsonMember(checkpoint, "phase").(string)
	return name
}

// sameState is the structural equality of two task states as their JSON forms (scheduler.ts jsonEqual of the states).
func sameState(left, right anyTaskState) bool {
	leftValue, leftErr := durable.ToJsonValue(left)
	rightValue, rightErr := durable.ToJsonValue(right)
	return leftErr == nil && rightErr == nil && jsonEqual(leftValue, rightValue)
}

// progressed is the record without its restart count, as after a state the task committed.
func progressed(record anyTaskRecord) anyTaskRecord {
	record.Restarts = 0
	return record
}

// jsonEqual is structural equality of two JSON values; object key order is ignored.
func jsonEqual(left, right durable.JsonValue) bool {
	switch typed := left.(type) {
	case *delta.JsonObject:
		other, ok := right.(*delta.JsonObject)
		if !ok || typed.Len() != other.Len() {
			return false
		}
		for key, value := range typed.All() {
			counterpart, present := other.Get(key)
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

// isSealed reports whether the close listener has run; it needs no mutex, which a step holds across registry reads.
func (s *TaskScheduler) isSealed() bool { return s.sealing.Load() }

// validateSleep checks that a sleep's until is a time on the Harness clock, and that an abort handler does not sleep.
func validateSleep(invocation *invocation, task durable.TaskId, until *float64) error {
	if invocation.mode == modeAbort {
		return fmt.Errorf("Abort handler of task %d cannot sleep", task)
	}
	if until == nil || math.IsNaN(*until) || math.IsInf(*until, 0) || *until < 0 {
		var shown string
		switch {
		case until == nil:
			shown = "undefined"
		case math.IsNaN(*until):
			shown = "NaN"
		case math.IsInf(*until, 1):
			shown = "Infinity"
		case math.IsInf(*until, -1):
			shown = "-Infinity"
		default:
			shown = jv.FormatNumber(*until) // ${until} (scheduler.ts:2070): -1e+21, -1e-7
		}
		return fmt.Errorf("Task %d cannot sleep until %s: not a time in milliseconds", task, shown)
	}
	return nil
}

// notDueLocked reports a sleep whose until the clock has not reached; an abort mark ends a sleep.
func (s *TaskScheduler) notDueLocked(record anyTaskRecord) bool {
	state := record.State
	return state.Status == durable.TaskSleeping && !record.AbortRequested && state.Until != nil && *state.Until > s.options.Now()
}

// earliestSleepLocked is wakeLocked's At: the earliest until of a sleep not yet due.
func (s *TaskScheduler) earliestSleepLocked() *float64 {
	var at *float64
	s.sleeping.Range(func(id durable.TaskId) bool {
		if record, live := s.live[id]; live && s.notDueLocked(record) && (at == nil || *record.State.Until < *at) {
			until := *record.State.Until
			at = &until
		}
		return true
	})
	return at
}

// wakeDueLocked makes every sleeping task whose time has come a reservation candidate.
func (s *TaskScheduler) wakeDueLocked() {
	s.sleeping.Range(func(id durable.TaskId) bool {
		if record, live := s.live[id]; live && !s.notDueLocked(record) {
			s.runnable.Add(id)
		}
		return true
	})
}

// scheduleWakeLocked recomputes the wake off the Session line: it arms the built-in timer, or tells OnWake of a change
// (scheduler.ts #scheduleWake, a microtask after the change). Changes until the recomputation runs share it, as they share
// upstream's one queued microtask.
func (s *TaskScheduler) scheduleWakeLocked() {
	if s.wakeScheduled || s.sealing.Load() {
		return
	}
	s.wakeScheduled = true
	// Upstream's microtask reads the wake right after the change, while a sleep just committed is still ahead; this goroutine
	// can run after it came due, when the scan no longer sees it. The earliest sleep at the change is taken now, as the
	// microtask sees it, so its timer is armed (and fires at once if due) instead of leaving the sleep for an unrelated pass.
	// With OnWake the whole value at the change is offered first, so a transient state is not lost either.
	var first *float64
	var firstWake *Wake
	if s.options.OnWake != nil {
		wake := s.wakeLocked()
		firstWake = &wake
	} else if s.enabled {
		first = s.earliestSleepLocked()
	}
	s.background.Go(func() { s.publishWake(first, firstWake) })
}

func (s *TaskScheduler) publishWake(first *float64, firstWake *Wake) {
	// One recomputation at a time, in the order scheduled: a stale value must not follow a newer one.
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	s.mu.Lock()
	s.wakeScheduled = false
	if s.sealing.Load() {
		s.mu.Unlock()
		// Upstream's microtask ran right after the change, before any later commit could fail the Session; the value
		// taken then is owed even if the seal won the race with this goroutine.
		if firstWake != nil {
			s.offerWake(*firstWake)
		}
		return
	}
	if s.options.OnWake == nil {
		// The built-in timer reads only wake().at, the earliest sleep not yet due, so it scans the sleeping tasks rather than
		// every live task: a scan per change over a large Session is quadratic.
		var at *float64
		if s.enabled {
			at = s.earliestSleepLocked()
			if first != nil && (at == nil || *first < *at) {
				at = first
			}
		}
		s.armTimerLocked(at)
		s.mu.Unlock()
		return
	}
	wake := s.wakeLocked()
	s.mu.Unlock()
	if firstWake != nil {
		s.offerWake(*firstWake)
	}
	if firstWake == nil || !equalWake(*firstWake, wake) {
		s.offerWake(wake)
	}
}

// offerWake gives OnWake a wake that differs from the last one it took. Only a delivered wake counts: after a panic, the next
// change or kick offers it again.
func (s *TaskScheduler) offerWake(wake Wake) {
	s.mu.Lock()
	last := s.lastWake
	s.mu.Unlock()
	if last != nil && equalWake(*last, wake) {
		return
	}
	err := func() (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if failure, ok := recovered.(error); ok {
					err = failure
				} else {
					err = errors.New(durable.ErrorMessage(recovered))
				}
			}
		}()
		s.options.OnWake(wake)
		return nil
	}()
	if err != nil {
		s.options.Report(err, onWakeInfo)
		return
	}
	s.mu.Lock()
	s.lastWake = &wake
	s.mu.Unlock()
}

func equalWake(a, b Wake) bool { return a.Active == b.Active && equalAt(a.At, b.At) }

func equalAt(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// armTimerLocked keeps the timer for the earliest sleep. It keeps the process alive, unlike the context expiry timer: a
// process with a sleeping task has work left, such as a retry backoff. Rechecks the clock when it fires, so a wake
// before until only arms it again.
func (s *TaskScheduler) armTimerLocked(at *float64) {
	if s.timer != nil && at != nil && s.timer.at == *at {
		return
	}
	if s.timer != nil {
		s.timer.disarm()
		s.timer = nil
	}
	if at == nil || s.sealing.Load() {
		return
	}
	timer := &wakeTimer{at: *at}
	timer.disarm = s.options.Timers.Foreground(func() {
		s.mu.Lock()
		if s.timer == timer {
			s.timer = nil
		}
		s.wakeDueLocked()
		s.mu.Unlock()
		s.kick()
	}, *at-s.options.Now())
	s.timer = timer
}

var (
	registryInfo = durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackRegistry}
	settingsInfo = durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackSettings}
	onWakeInfo   = durable.ReportInfo{Type: durable.ReportHostError, Callback: durable.HostCallbackOnWake}
)

// installerOf is the extension that installed task in snapshot, the last one that lists it; empty for a built-in task.
func installerOf(snapshot durable.RegistrySnapshot, task durable.AnyTask) string {
	if task.Definition == nil {
		return ""
	}
	installed := snapshot.Installed()
	for _, extension := range slices.Backward(installed) {
		if slices.ContainsFunc(extension.Tasks, func(candidate durable.AnyTask) bool { return sameTaskDefinition(candidate, task) }) {
			return extension.Name
		}
	}
	return ""
}

// reportTask is the task of an invocation as a report names it.
func (invocation *invocation) reportTask() *durable.ReportTask {
	return &durable.ReportTask{Id: invocation.taskId, Kind: invocation.kind, ConversationId: invocation.conversationId}
}

// aboutInvocation is the report info of a failure of an invocation's task: the task, and the extension that installed the
// definition the invocation runs.
func (s *TaskScheduler) aboutInvocation(reportType durable.ReportType, invocation *invocation) durable.ReportInfo {
	return durable.ReportInfo{Type: reportType, Task: invocation.reportTask(), Extension: invocation.extension}
}

// aboutRecord is the report info of a failure of a stored task, with the extension that installed its current
// definition: the code that failed is that extension's. Built-in tasks have none. A registry that panics is reported where
// it is read for scheduling; a report goes out without the name.
func (s *TaskScheduler) aboutRecord(reportType durable.ReportType, record anyTaskRecord) durable.ReportInfo {
	info := durable.ReportInfo{Type: reportType, Task: &durable.ReportTask{Id: record.Id, Kind: record.Kind, ConversationId: record.ConversationId}}
	func() {
		defer func() {
			// upstream: packages/durable/src/harness/scheduler.ts:#aboutTask
			if recovered := recover(); recovered != nil {
				info.Extension = ""
			}
		}()
		snapshot := s.options.Registry.Snapshot()
		info.Extension = installerOf(snapshot, snapshot.Task(record.Kind))
	}()
	return info
}

// parkedLocked reports a task cut off restartLimit times in a row; an abort mark still runs its abort handler.
func (s *TaskScheduler) parkedLocked(abortRequested bool, restarts int) bool {
	return !abortRequested && restarts > 0 && float64(restarts) >= s.restartLimit()
}

// unparkRaisedLocked makes the parked tasks that a raised settings.restartLimit no longer parks candidates again, and
// tells the graph.
func (s *TaskScheduler) unparkRaisedLocked() {
	unparked := false
	for _, id := range s.parkedTasks.Values() {
		record, live := s.live[id]
		if live && s.parkedLocked(record.AbortRequested, record.Restarts) {
			continue
		}
		s.parkedTasks.Delete(id)
		if !live {
			continue
		}
		s.runnable.Add(id)
		unparked = true
	}
	if unparked {
		s.notifyBlockedLocked()
	}
}

// restartLimit is settings.restartLimit, or the default when the host's settings panic.
func (s *TaskScheduler) restartLimit() float64 {
	limit, err := func() (limit float64, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = env.ToError(recovered)
			}
		}()
		return s.options.Settings().RestartLimit, nil
	}()
	if err != nil {
		s.options.Report(err, settingsInfo)
		return durable.DefaultRestartLimit
	}
	return limit
}
