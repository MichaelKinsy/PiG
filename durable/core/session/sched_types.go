package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// Cancel is a cancellation signal: the AbortSignal of an invocation or of a
// caller's context.
type Cancel struct {
	aborted bool
	reason  error // AbortSignal.reason: ErrAborted for abort(), else the reason passed to AbortWith
	hooks   []func()
}

// Aborted reports whether Abort ran.
func (c *Cancel) Aborted() bool { return c != nil && c.aborted }

// Abort marks the signal aborted with the default reason (AbortController.abort()) and runs the registered hooks once.
func (c *Cancel) Abort() { c.AbortWith(ErrAborted) }

// Err is the reason of an aborted signal (AbortSignal.throwIfAborted), or nil.
func (c *Cancel) Err() error {
	if c == nil || !c.aborted {
		return nil
	}
	return c.reason
}

// AbortWith marks the signal aborted with reason (AbortController.abort(reason)); a signal keeps its first reason.
func (c *Cancel) AbortWith(reason error) {
	if c.aborted {
		return
	}
	c.aborted, c.reason = true, reason
	hooks := c.hooks
	c.hooks = nil
	for _, h := range hooks {
		h()
	}
}

// OnAbort registers fn to run on abort and returns a function that removes it.
func (c *Cancel) OnAbort(fn func()) (remove func()) {
	if c == nil {
		return func() {}
	}
	if c.aborted {
		fn()
		return func() {}
	}
	c.hooks = append(c.hooks, fn)
	at := len(c.hooks) - 1
	return func() {
		if at < len(c.hooks) {
			c.hooks[at] = func() {}
		}
	}
}

// Task errors reported by the scheduler.
var (
	// ErrHarnessClosed is Pi's closedError.
	// Read-only: assigned once at initialization.
	ErrHarnessClosed = errors.New("Harness is closed")
	// ErrAborted reports a cancelled wait.
	// Read-only: assigned once at initialization.
	ErrAborted = errors.New("The operation was aborted")
)

// TaskImpl is an executable task definition registered in the registry.
type TaskImpl struct {
	Def TaskDef
	// Extension is the name of the extension that installed the definition; empty for a built-in task.
	Extension string
	// Migrate converts a record stored by an older version; nil when the
	// definition has none. It runs at reservation.
	Migrate func(input, checkpoint any, fromVersion int) (newInput, newCheckpoint any, err error)
	// MigrateAsync is Migrate for a definition whose migrate() is host code: it starts the migration and calls done once,
	// later, with its result. The scheduler asks again after done (it kicks), so a reservation that needed a migration
	// is retried with the answer in hand and never blocks the Session line on the host.
	MigrateAsync func(input, checkpoint any, fromVersion int, done func(newInput, newCheckpoint any, err error))
	// Phase runs the handler for the checkpoint's phase of a running task and
	// calls done once, with the handler's error or nil. The handler drives the
	// runtime and ends by committing durable progress.
	Phase func(rt *Runtime, task *jv.Object, done func(error))
	// Abort runs in a fresh invocation after an abort mark and must commit a
	// terminal outcome.
	Abort func(rt *Runtime, task *jv.Object, done func(error))
	// AbortReturnHops is the hops between the end of Abort's final commit job and `#runAbort`'s step enqueue when the
	// handler is an async function of Pi that awaits that commit: the commit's `#gated` await, the handler's own await and
	// its async return, then runAbort's await (scheduler.ts :1399-1411). A handler that runs in the host spends these hops
	// itself and leaves it zero.
	AbortReturnHops int
}

// Snapshot is a registry snapshot: the task definitions of one phase.
type Snapshot interface {
	Task(name string) *TaskImpl
}

// Env connects the scheduler to its host.
type Env struct {
	// Snapshot returns the current registry snapshot.
	Snapshot func() Snapshot
	// FreshSnapshot, when set, is the registry.snapshot() call of a step's decide (scheduler.ts:955), which runs user
	// code inside the step job: it returns a *NeedRead with no reads while the host is asked, and the Loop reruns the
	// job once the host answered. Unset, decide uses Snapshot.
	FreshSnapshot func() (Snapshot, error)
	// ContextRetentionMs is settings.contextRetentionMs: how long a conversation's kept context range outlives its idleness.
	ContextRetentionMs func() float64
	// ReleaseContext, when set, is told each conversation whose kept context range the scheduler drops for idleness
	// (scheduler.ts #settleIdle): what the core derived for it may go, as Pi's range does. It runs at an idle check, never
	// while a caller still reads a view of that conversation.
	ReleaseContext func(conversationID ID)
	// Report forwards a non-fatal failure to HarnessOptions.onReport; info says what happened (types.ts ReportInfo as
	// an object: type, and task, extension, reason, callback or conversationId where they apply).
	Report func(err error, info *jv.Object)
	// RestartLimit is settings.restartLimit, read at each use; nil selects the default.
	RestartLimit func() float64
	// BlockedChanged is told when blocked reasons may have changed without a commit: a registry change, a failed
	// migration, or a task newly parked or no longer parked (scheduler.ts #notifyBlocked).
	BlockedChanged func()
	// Now is the Harness clock.
	Now func() float64
	// Timer arms a background timer that calls fire at the Harness time at (a timer effect, ABI section 6) and returns its
	// cancel function: it only frees memory, and a host may keep none.
	Timer func(at float64, fire func()) (cancel func())
	// WakeTimer arms the timer that resumes the earliest sleeping task (a durable timer effect, ABI section 6): a host
	// that wakes its process itself keeps the object alarm at its time. Used while OnWake is nil.
	WakeTimer func(at float64, fire func()) (cancel func())
	// OnWake, when set (SetOnWake), replaces WakeTimer: the scheduler offers it each recomputed Wake (HarnessOptions.onWake)
	// and arms no timer for sleeping tasks. The host drops a value it already delivered.
	OnWake func(w Wake)
	// SettleOutcome runs in the transaction that settles a task with a
	// scheduler-written outcome (faulted or orphaned): harness.live.ts settleSchedulerOutcome.
	SettleOutcome func(tx *Tx, record *jv.Object, outcome *jv.Object) error
	// WithdrawInputs runs in the transaction that withdraws a conversation's
	// queued inputs (inbox.ts withdrawQueuedInputs).
	WithdrawInputs func(tx *Tx, conversationID ID) error
	// ReadTicks is the ticks the running line job's Storage reads have taken so far, and After runs then once a chain of n
	// hops ran: a reconcile a chain load schedules after its read starts when Pi's microtask does, not at the job's start.
	ReadTicks func() int
	// GateRead asks the host's Storage to run the Pi Storage read of method (task, conversation) that a chain load makes, so a Storage that
	// counts, holds or fails it sees it (scheduler.ts #loadChain); nil asks nothing.
	GateRead func(method string, id ID)
	After    func(n int, then func())
}

// taskNode is the part of a task the ownership walk needs.
type taskNode struct {
	conversationID ID
	owner          ID // 0: conversation-owned
	background     bool
}

func nodeOf(rec *jv.Object) taskNode {
	owner, _ := getInt(rec, "owner")
	return taskNode{conversationID: mustInt(rec, "conversationId"), owner: owner, background: getBool(rec, "background")}
}

// up is the parent step of the ownership walk: a task or a conversation.
type up struct {
	isTask bool
	id     ID
}

func parentOf(n taskNode) up {
	if n.owner != 0 {
		return up{isTask: true, id: n.owner}
	}
	return up{id: n.conversationID}
}

// step is one hop of the walk above a task.
type step struct {
	kind uint8 // stepTask, stepConversation, stepUnknown
	id   ID
	node taskNode
}

const (
	stepTask uint8 = iota + 1
	stepConversation
	stepUnknown
)

// taskMap is an insertion-ordered map of task records (a JavaScript Map).
type taskMap struct {
	order *orderedSet[ID]
	m     map[ID]*jv.Object
}

func newTaskMap() *taskMap { return &taskMap{order: newOrderedSet[ID](), m: map[ID]*jv.Object{}} }

func (t *taskMap) get(id ID) (*jv.Object, bool) { r, ok := t.m[id]; return r, ok }

func (t *taskMap) set(id ID, rec *jv.Object) {
	t.order.Add(id)
	t.m[id] = rec
}

func (t *taskMap) delete(id ID) {
	if _, ok := t.m[id]; !ok {
		return
	}
	delete(t.m, id)
	t.order.Delete(id)
}

// values returns the records in insertion order, as a snapshot slice.
func (t *taskMap) values() []*jv.Object {
	out := make([]*jv.Object, 0, t.order.Len())
	t.order.Range(func(id ID) bool {
		out = append(out, t.m[id])
		return true
	})
	return out
}

// taskWaiter waits for a task to settle.
type taskWaiter struct {
	cancel *Cancel
	remove func()
	done   func(rec *jv.Object, err error)
}

// idleWaiter waits for a conversation to go idle.
type idleWaiter struct {
	remove func()
	done   func(error)
}

func cancellationIntent(rec *jv.Object) bool {
	return taskStatus(rec) != StatusTerminal && (getBool(rec, "abortRequested") || failedOutcome(rec))
}

func failedOutcome(rec *jv.Object) bool {
	state := getObj(rec, "state")
	st, _ := getStr(state, "status")
	if st != StatusCompleting && st != StatusTerminal {
		return false
	}
	outcome := getObj(state, "outcome")
	os, _ := getStr(outcome, "status")
	return os != "completed"
}

func memoOf(rec *jv.Object, name string) (any, bool) {
	if rec == nil {
		return nil, false
	}
	m := getObj(rec, "memos")
	if m == nil {
		return nil, false
	}
	return m.Get(name)
}
