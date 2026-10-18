package session

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

const schedScanPage = 256

// Read-only: assigned once at initialization.
var liveStatuses = [...]string{StatusPending, StatusRunning, StatusWaiting, StatusSleeping, StatusCompleting}

type invocation struct {
	taskID ID
	kind   string
	// extension is the extension that installed the definition this invocation runs, as of its reservation; empty for a
	// built-in task.
	extension      string
	conversationID ID
	abortMode      bool
	signal         *Cancel
	ended          bool
	finished       bool
	doneWaiters    []func()
	watches        []func()
}

func (inv *invocation) finish() {
	if inv.finished {
		return
	}
	inv.finished = true
	w := inv.doneWaiters
	inv.doneWaiters = nil
	for _, f := range w {
		f()
	}
}

type reservation struct {
	inv      *invocation
	task     *TaskImpl
	snapshot Snapshot
}

type resolution struct {
	// pending is set while a host-run migration has not answered: the task is neither blocked nor ready yet.
	pending bool
	blocked string // "" when ready
	task    *TaskImpl
	record  *jv.Object
}

// migration is the state of one host-run migration (TaskImpl.MigrateAsync): started once per task and definition, then
// answered.
type migration struct {
	task      *TaskImpl
	from      int
	done      bool
	input, cp any
	err       error
}

type failedMigration struct {
	task *TaskImpl
	err  error
}

type overlay struct {
	tasks map[ID]*jv.Object
	order []ID
	edges map[ID]ID // conversation -> owner task, 0 for none
	// links are the links to the staged tasks below does not list, by parent node, for walks down; built at first use.
	links map[treeNode]*orderedSet[treeNode]
}

// Scheduler is pi-durable's TaskScheduler (harness/scheduler.ts) as a
// synchronous state machine over a Session and a Loop: it keeps the live
// tasks, reserves them for invocation, runs their phases through
// continuation-passing handlers, and settles ownership, abort and join rules.
type Scheduler struct {
	sess *Session
	loop *Loop
	env  Env

	live         *taskMap
	invocations  map[ID]*invocation
	taskWaiters  map[ID][]*taskWaiter
	taskWaitKeys []ID
	idleWaiters  map[ID][]*idleWaiter
	idleWaitKeys []ID

	contexts map[ID]*keptContext
	expiry   struct {
		set    bool
		at     float64
		cancel func()
	}

	failedMigrations map[ID]failedMigration
	migrations       map[ID]*migration
	edges            map[ID]ID // conversation -> owner task (0: none); presence means known
	settled          map[ID]taskNode
	failFastChecks   []ID
	// below is the tree downward around live work: each node's child nodes that are live tasks or have nodes listed below
	// them. A conversation's parent is its owner task; ownerless ones with work are in roots.
	below map[treeNode]*orderedSet[treeNode]
	// belowEntries is the number of children listed in below, kept as they are listed so indexSizes costs nothing per step.
	belowEntries int
	roots        *orderedSet[ID]
	// unloaded holds the known tasks whose chain may not be loaded; every other known task's chain is, and stays, loaded.
	unloaded *orderedSet[ID]
	// abandoned holds the live tasks with abandonOnRestart found at open, from an earlier Harness. The first reservation
	// pass abort-marks them with reason restart, so nothing of them or below them runs a phase again.
	abandoned *orderedSet[ID]
	// intent holds the live tasks with cancellation intent, the sources of abort cascades (spec §5.4).
	intent *orderedSet[ID]
	// runnable holds the live tasks that are not completing, the candidates of a reservation pass; ones found waiting
	// leave it.
	runnable *orderedSet[ID]
	// finalizeChecks holds the completing tasks the next finalize pass checks: those that started holding, and holders of
	// ended work.
	finalizeChecks *orderedSet[ID]
	// waiters holds the live waiting tasks by each task in their on.
	waiters map[ID]*orderedSet[ID]
	// sleeping holds the sleeping live tasks, due or not: the candidates of wakeDue.
	sleeping *orderedSet[ID]
	// wakeTimer is the timer for the earliest sleep, without OnWake while scheduling runs.
	wakeTimer struct {
		set    bool
		at     float64
		cancel func()
	}
	wakeScheduled bool
	// parkedTasks holds the tasks a pass left out as parked; each pass checks them again, as settings.restartLimit may
	// have risen.
	parkedTasks *orderedSet[ID]
	// reconcilingWork counts the reconcile passes underway with work: their commits finalize held outcomes and mark
	// cascades.
	reconcilingWork int
	// snapshotReported is set once a registry snapshot that failed for the task graph was reported.
	// dropQueue holds the ended tasks and conversations found unneeded, which sweep drops at the end of the commit's
	// publication unless needed again by then: a chain load installs nodes before the work below them is listed.
	dropQueue      *orderedSet[treeNode]
	sweepScheduled bool

	reconcileScheduled bool
	cascadePending     bool
	enabled            bool
	closing            bool
	dirty              bool
	draining           bool
}

// NewScheduler returns a scheduler over the Session, on the Loop.
func NewScheduler(sess *Session, loop *Loop, env Env) *Scheduler {
	s := &Scheduler{
		sess: sess, loop: loop, env: env,
		live: newTaskMap(), invocations: map[ID]*invocation{},
		taskWaiters: map[ID][]*taskWaiter{}, idleWaiters: map[ID][]*idleWaiter{},
		contexts: map[ID]*keptContext{}, failedMigrations: map[ID]failedMigration{}, migrations: map[ID]*migration{}, edges: map[ID]ID{}, settled: map[ID]taskNode{},
		below: map[treeNode]*orderedSet[treeNode]{}, roots: newOrderedSet[ID](), unloaded: newOrderedSet[ID](), intent: newOrderedSet[ID](), runnable: newOrderedSet[ID](), finalizeChecks: newOrderedSet[ID](), waiters: map[ID]*orderedSet[ID]{}, sleeping: newOrderedSet[ID](), dropQueue: newOrderedSet[treeNode](), abandoned: newOrderedSet[ID](), parkedTasks: newOrderedSet[ID](),
	}
	sess.OnFail = func(err error) { s.report(err, obj("type", "session_failed")) }
	return s
}

// failSession fails the Session, which reports it, for a failure in one of the scheduler's own commits: no extension code runs
// in them, so a failure is a storage failure, a host callback, or a bug, and running the pass again fixes none. Ignored once
// closing (scheduler.ts #failSession).
func (s *Scheduler) failSession(err error) {
	if !s.closing {
		s.sess.fail(err)
	}
}

// closedErr is what a wait rejects with when the Harness closes: SessionFailed when the Session failed.
func (s *Scheduler) closedErr() error {
	if s.sess.failure != nil {
		return &FailedError{Cause: s.sess.failure}
	}
	return ErrHarnessClosed
}

func (s *Scheduler) report(err error, info *jv.Object) {
	if s.env.Report != nil {
		s.env.Report(err, info)
	}
}

// restartLimit is settings.restartLimit, 5 unless the host set another.
func (s *Scheduler) restartLimit() float64 {
	if s.env.RestartLimit != nil {
		return s.env.RestartLimit()
	}
	return defaultRestartLimit
}

// defaultRestartLimit is the default of settings.restartLimit.
const defaultRestartLimit = 5

// parked is a task cut off restartLimit times in a row; an abort mark still runs its abort handler (scheduler.ts #parked).
func (s *Scheduler) parked(rec *jv.Object) bool {
	return !getBool(rec, "abortRequested") && restartsOf(rec) > 0 && restartsOf(rec) >= s.restartLimit()
}

// notifyBlocked tells the host that blocked reasons may have changed without a commit.
func (s *Scheduler) notifyBlocked() {
	if s.env.BlockedChanged != nil {
		s.env.BlockedChanged()
	}
}

// reportTask is the task of a report: {id, kind, conversationId}.
func reportTask(id ID, kind string, conversationID ID) *jv.Object {
	return obj("id", float64(id), "kind", kind, "conversationId", float64(conversationID))
}

// aboutRecord is the report info of a failure of a stored task, with the extension that installed its current
// definition: the code that failed is that extension's. Built-in tasks have none (scheduler.ts #aboutTask).
func (s *Scheduler) aboutRecord(typ string, rec *jv.Object) *jv.Object {
	kind, _ := getStr(rec, "kind")
	info := obj("type", typ, "task", reportTask(mustInt(rec, "id"), kind, mustInt(rec, "conversationId")))
	if task := s.env.Snapshot().Task(kind); task != nil && task.Extension != "" {
		info.Set("extension", task.Extension)
	}
	return info
}

// aboutInvocation is the report info of a failure of an invocation's task: the task, and the extension that installed
// the definition the invocation runs.
func aboutInvocation(typ string, inv *invocation) *jv.Object {
	info := obj("type", typ, "task", reportTask(inv.taskID, inv.kind, inv.conversationID))
	if inv.extension != "" {
		info.Set("extension", inv.extension)
	}
	return info
}

// commit runs change as a job on the Session line and calls then with its
// error. The commit's publication reaches observe through the Session's
// listener before then runs.
func (s *Scheduler) commit(change func(tx *Tx) error, scope TxScope, then func(error)) {
	s.commitWith(nil, change, scope, then)
}

// commitWith is commit under a caller's signal: the job rejects with the signal's reason when it was aborted before
// the job starts (session.ts #runCommit throwIfAborted).
func (s *Scheduler) commitWith(cancel *Cancel, change func(tx *Tx) error, scope TxScope, then func(error)) {
	s.commitJob(s.loop.LineAfterTail, cancel, change, scope, then)
}

// commitQueued is commit for a call a caller makes through `Session.commitWith` (session.ts :95-102): on an idle line the job starts
// a tick after the call.
func (s *Scheduler) commitQueued(change func(tx *Tx) error, scope TxScope, then func(error)) {
	s.commitJob(s.loop.LineAfterTail, nil, change, scope, then)
}

func (s *Scheduler) commitJob(line func(func() error, func(error)), cancel *Cancel, change func(tx *Tx) error, scope TxScope, then func(error)) {
	s.commitJobAfter(line, cancel, change, scope, nil, then)
}

// commitJobAfter is commitJob that also runs after, when the commit succeeded, as the job completes: right after the commit's
// publication, before the line's next job.
func (s *Scheduler) commitJobAfter(line func(func() error, func(error)), cancel *Cancel, change func(tx *Tx) error, scope TxScope, after func(), then func(error)) {
	line(func() error {
		if err := cancel.Err(); err != nil {
			return err
		}
		_, err := s.sess.RunCommit(change, scope)
		if err == nil && after != nil {
			s.loop.OnComplete(after)
		}
		return err
	}, then)
}

func (s *Scheduler) scanAllTasks(tx *Tx, q TaskQuery) ([]*jv.Object, error) {
	var out []*jv.Object
	var after ID
	for {
		page, err := tx.ScanTasks(q, schedScanPage, after)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.After == 0 {
			return out, nil
		}
		after = page.After
	}
}

// Open loads the live tasks in one commit, resets running tasks to pending and
// arms reconciliation. The scheduler observes the Session's commits from here on.
func (s *Scheduler) Open(done func(error)) {
	s.sess.SubscribeCommits(s.observe)
	s.sess.SubscribeClose(s.Seal)
	limit := s.restartLimit()
	var parked []*jv.Object
	s.commit(func(tx *Tx) error {
		parked = nil
		var scans [][]*jv.Object
		for _, st := range liveStatuses {
			recs, err := s.scanAllTasks(tx, TaskQuery{Status: st})
			if err != nil {
				return err
			}
			scans = append(scans, recs)
		}
		for _, recs := range scans {
			for _, rec := range recs {
				s.track(nil, rec)
				if getBool(rec, "abandonOnRestart") {
					s.abandoned.Add(mustInt(rec, "id"))
				}
				state := getObj(rec, "state")
				if taskStatus(rec) == StatusRunning {
					cp, _ := state.Get("checkpoint")
					restarts := restartsOf(rec) + 1
					pending := with(withState(rec, obj("status", StatusPending, "checkpoint", cp)), "restarts", restarts)
					if err := tx.stageTask(pending); err != nil {
						return err
					}
					if restarts >= limit && !getBool(rec, "abortRequested") {
						parked = append(parked, rec)
					}
				}
				if taskStatus(rec) == StatusWaiting {
					if pol, _ := getStr(state, "policy"); pol == "failFast" {
						s.addFailFast(mustInt(rec, "id"))
					}
				}
			}
		}
		return nil
	}, TxScope{}, func(err error) {
		if err == nil {
			for _, rec := range parked {
				kind, _ := getStr(rec, "kind")
				restarts := restartsOf(rec) + 1
				s.report(errorf("Task %d (%s) was cut off %d times without progress; it is parked until retryTask()", mustInt(rec, "id"), kind, restarts), s.aboutRecord("task_parked", rec))
			}
			s.cascadePending = true
			s.scheduleReconcile()
			s.scheduleWake()
		}
		done(err)
	})
}

// Retry runs a task parked by the restart limit again: it clears its restart count and enables scheduling. It does nothing
// for a task that is not parked, and fails for an unknown one (scheduler.ts retry).
func (s *Scheduler) Retry(id ID, cancel *Cancel, then func(error)) {
	s.commitWith(cancel, func(tx *Tx) error {
		current, err := tx.Task(id)
		if err != nil {
			return err
		}
		if current == nil {
			return errorf("Task %d does not exist", id)
		}
		if st := taskStatus(current); st == StatusTerminal || st == StatusCompleting || !s.parked(current) {
			return nil
		}
		return tx.stageTask(progressed(current))
	}, TxScope{}, func(err error) {
		if err == nil {
			s.Resume()
		}
		then(err)
	})
}

func (s *Scheduler) addFailFast(id ID) {
	if !slices.Contains(s.failFastChecks, id) {
		s.failFastChecks = append(s.failFastChecks, id)
	}
}

// Resume enables scheduling, makes sleeping tasks that are due runnable (a host wakeup, such as an alarm, calls this), and
// starts any reservable task.
func (s *Scheduler) Resume() {
	s.enabled = true
	s.wakeDue()
	s.kick()
}

// RegistryChanged is the registry subscription: a new snapshot may make blocked tasks runnable.
func (s *Scheduler) RegistryChanged() { s.kick() }

// Join calls then once every running invocation has ended.
func (s *Scheduler) Join(then func()) {
	var pending []*invocation
	for _, inv := range s.invocations {
		pending = append(pending, inv)
	}

	slices.SortFunc(pending, func(a, b *invocation) int { return int(a.taskID - b.taskID) })
	remaining := 0
	for _, inv := range pending {
		if !inv.finished {
			remaining++
		}
	}
	if remaining == 0 {
		s.loop.Defer(then)
		return
	}
	for _, inv := range pending {
		if inv.finished {
			continue
		}
		inv.doneWaiters = append(inv.doneWaiters, func() {
			remaining--
			if remaining == 0 {
				s.loop.Defer(then)
			}
		})
	}
}

// Abort marks a task for abort. then receives "marked" or "terminal"; for a
// task whose invocation runs, it is called after that invocation ended.
func (s *Scheduler) Abort(id ID, keepRestart bool, cancel *Cancel, then func(result string, err error)) {
	var result string
	var run *invocation
	s.commitWith(cancel, func(tx *Tx) error {
		result, run = "", nil
		current, err := tx.Task(id)
		if err != nil {
			return err
		}
		if current == nil {
			return errorf("Task %d does not exist", id)
		}
		if taskStatus(current) == StatusTerminal {
			result = "terminal"
			return nil
		}
		inv := s.invocations[id]
		// A restart-marked task has no run invocation; it only waits for its abort.
		if keepRestart && abortReasonOf(current) == "restart" {
			result = "marked"
			return nil
		}
		if inv == nil && taskStatus(current) != StatusCompleting {
			if _, err := s.loadScopes(false); err != nil {
				return err
			}
			if !s.hasOwnedLive(id, nil, nil) {
				res := s.resolve(current, s.env.Snapshot())
				if res.blocked != "" {
					if err := s.terminate(tx, current, obj("status", "orphaned", "reason", res.blocked)); err != nil {
						return err
					}
					result = "marked"
					return nil
				}
			}
		}
		if !getBool(current, "abortRequested") || current.Has("abortReason") {
			if err := tx.stageTask(withAbortMark(current, "")); err != nil {
				return err
			}
		}
		result = "marked"
		if inv != nil && !inv.abortMode {
			run = inv
		}
		return nil
	}, TxScope{}, func(err error) {
		if err != nil {
			then("", err)
			return
		}
		if run == nil {
			then(result, nil)
			return
		}
		s.awaitInvocation(run, cancel, func(e error) { then(result, e) })
	})
}

func (s *Scheduler) awaitInvocation(inv *invocation, cancel *Cancel, then func(error)) {
	settled := false
	var remove func()
	fire := func(err error) {
		if settled {
			return
		}
		settled = true
		if remove != nil {
			remove()
		}
		s.loop.Defer(func() { then(err) })
	}
	if inv.finished {
		fire(nil)
		return
	}
	// scheduler.ts abort: Promise.race([run.done, session.failed]); an invocation that ignores its signal can outlive a failed Session.
	if s.sess.failure != nil {
		fire(&FailedError{Cause: s.sess.failure})
		return
	}
	unfail := s.sess.OnFailed(func(cause error) { fire(&FailedError{Cause: cause}) })
	inv.doneWaiters = append(inv.doneWaiters, func() { fire(nil) })
	var unabort func()
	remove = func() {
		unfail()
		if unabort != nil {
			unabort()
		}
	}
	if cancel != nil {
		unabort = cancel.OnAbort(func() { fire(ErrAborted) })
	}
}

// WaitForTask calls then with the task's terminal record. A task that is not
// live is read from the store on the line.
func (s *Scheduler) WaitForTask(id ID, cancel *Cancel, then func(rec *jv.Object, err error)) {
	s.WaitForTaskFound(id, cancel, func(rec *jv.Object, err error, _ bool) { then(rec, err) })
}

// WaitForTaskFound is WaitForTask for a host caller: a read job on the line (readOnLine, scheduler.ts :306-316) whose caller
// resumes at the job's run and returns `found.promise`. then reports whether the record came from a waiter the job registered
// (a live task, resolved by the terminal publication) or from the store (an already settled promise).
func (s *Scheduler) WaitForTaskFound(id ID, cancel *Cancel, then func(rec *jv.Object, err error, waited bool)) {
	var found *jv.Object
	var wait bool
	s.loop.ClientReadLine(func() error {
		found, wait = nil, false
		if s.closing {
			return s.closedErr()
		}
		if _, live := s.live.get(id); live {
			wait = true
			return nil
		}
		rec, err := s.sess.store.Task(id)
		if err != nil {
			return err
		}
		if rec == nil {
			return errorf("Task %d does not exist", id)
		}
		found = rec
		return nil
	}, func(err error) {
		switch {
		case err != nil:
			then(nil, err, false)
		case wait:
			s.addTaskWaiter(id, cancel, func(rec *jv.Object, err error) { then(rec, err, true) })
		default:
			then(found, nil, false)
		}
	})
}

func (s *Scheduler) addTaskWaiter(id ID, cancel *Cancel, then func(*jv.Object, error)) {
	if cancel.Aborted() {
		s.loop.Defer(func() { then(nil, ErrAborted) })
		return
	}
	w := &taskWaiter{cancel: cancel, done: then}
	if _, ok := s.taskWaiters[id]; !ok {
		s.taskWaitKeys = append(s.taskWaitKeys, id)
	}
	s.taskWaiters[id] = append(s.taskWaiters[id], w)
	if cancel != nil {
		w.remove = cancel.OnAbort(func() {
			list := s.taskWaiters[id]
			for i, x := range list {
				if x == w {
					s.taskWaiters[id] = append(list[:i:i], list[i+1:]...)
					break
				}
			}
			if len(s.taskWaiters[id]) == 0 {
				s.dropTaskKey(id)
			}
			s.loop.Defer(func() { then(nil, ErrAborted) })
		})
	}
}

func (s *Scheduler) dropTaskKey(id ID) {
	delete(s.taskWaiters, id)
	s.taskWaitKeys = slices.DeleteFunc(s.taskWaitKeys, func(k ID) bool { return k == id })
}

func (s *Scheduler) resolveTaskWaiters(id ID, rec *jv.Object) {
	list := s.taskWaiters[id]
	s.dropTaskKey(id)
	for _, w := range list {
		if w.remove != nil {
			w.remove()
		}
		w := w
		// waitForTask is an async function returning the waiter's promise: its own promise settles
		// two reaction jobs after the waiter does.
		s.loop.Defer(func() { s.loop.Defer(func() { w.done(rec, nil) }) })
	}
}

// WaitForIdle calls then when the conversation (0: every root) has no live
// non-background task or queued input below it.
func (s *Scheduler) WaitForIdle(conversationID ID, cancel *Cancel, then func(error)) {
	if s.closing {
		s.loop.Defer(func() { then(s.closedErr()) })
		return
	}
	if s.idle(conversationID) {
		s.loop.Defer(func() { then(nil) })
		return
	}
	s.scheduleReconcile()
	if cancel.Aborted() {
		s.loop.Defer(func() { then(ErrAborted) })
		return
	}
	w := &idleWaiter{done: then}
	if _, ok := s.idleWaiters[conversationID]; !ok {
		s.idleWaitKeys = append(s.idleWaitKeys, conversationID)
	}
	s.idleWaiters[conversationID] = append(s.idleWaiters[conversationID], w)
	if cancel != nil {
		w.remove = cancel.OnAbort(func() {
			list := s.idleWaiters[conversationID]
			for i, x := range list {
				if x == w {
					s.idleWaiters[conversationID] = append(list[:i:i], list[i+1:]...)
					break
				}
			}
			if len(s.idleWaiters[conversationID]) == 0 {
				s.dropIdleKey(conversationID)
			}
			s.loop.Defer(func() { then(ErrAborted) })
		})
	}
}

func (s *Scheduler) dropIdleKey(id ID) {
	delete(s.idleWaiters, id)
	s.idleWaitKeys = slices.DeleteFunc(s.idleWaitKeys, func(k ID) bool { return k == id })
}

// AbortConversation marks every task below a conversation for abort and waits
// for idle; with background, background tasks too, waiting for each.
func (s *Scheduler) AbortConversation(conversationID ID, background bool, cancel *Cancel, then func(error)) {
	var reached []ID
	s.commitWith(cancel, func(tx *Tx) error {
		reached = nil
		queued, err := s.loadScopes(true)
		if err != nil {
			return err
		}
		sc := scopeConv(conversationID)
		var failed error
		s.walkDown(conversationTreeNode(conversationID), walkOptions{crossBackground: background}, func(id ID, n taskNode, rec *jv.Object) bool {
			if rec == nil || (n.background && !background) {
				return true
			}
			reached = append(reached, id)
			if !getBool(rec, "abortRequested") || rec.Has("abortReason") {
				failed = tx.stageTask(withAbortMark(rec, ""))
			}
			return failed == nil
		})
		if failed != nil {
			return failed
		}
		for _, id := range queued {
			if s.inScope(up{id: id}, sc, background) == triTrue {
				if err := s.env.WithdrawInputs(tx, id); err != nil {
					return err
				}
			}
		}
		return nil
	}, TxScope{}, func(err error) {
		if err != nil {
			then(err)
			return
		}
		var wait func(i int)
		wait = func(i int) {
			if !background || i >= len(reached) {
				s.WaitForIdle(conversationID, cancel, then)
				return
			}
			s.WaitForTask(reached[i], cancel, func(_ *jv.Object, err error) {
				if err != nil {
					then(err)
					return
				}
				wait(i + 1)
			})
		}
		wait(0)
	})
}

// Bound is the signal of an operation made through an invocation-bound handle (harness.ts boundConversation,
// withAbortSignal(binding.signal, ...)): the operation's line job checks it at start (session.ts:411 throwIfAborted). A
// task without a running invocation yields a signal already aborted as ended.
func (s *Scheduler) Bound(taskID ID) *Cancel {
	if inv := s.invocations[taskID]; inv != nil {
		return inv.signal
	}
	c := &Cancel{}
	c.AbortWith(errorf("Task %d invocation has ended", taskID))
	return c
}

// Seal stops scheduling: waiters reject, and the invocation signals abort in a microtask (signalInvocations).
func (s *Scheduler) Seal() {
	s.closing = true
	// session.ts close queues `Promise.resolve().then(beforeClose)` before it runs the close listeners, and beforeClose is join(),
	// which signals every invocation: one microtask on, ahead of the reactions of the waits this seal rejects, not inside it.
	s.loop.Defer(s.signalInvocations)
	s.contexts = map[ID]*keptContext{}
	s.cancelExpiry()
	s.cancelWakeTimer()
	for _, id := range slices.Clone(s.taskWaitKeys) {
		for _, w := range s.taskWaiters[id] {
			if w.remove != nil {
				w.remove()
			}
			err := s.closedErr()
			s.loop.Defer(func() { w.done(nil, err) })
		}
	}
	s.taskWaiters, s.taskWaitKeys = map[ID][]*taskWaiter{}, nil
	for _, id := range slices.Clone(s.idleWaitKeys) {
		for _, w := range s.idleWaiters[id] {
			if w.remove != nil {
				w.remove()
			}
			err := s.closedErr()
			s.loop.Defer(func() { w.done(err) })
		}
	}
	s.idleWaiters, s.idleWaitKeys = map[ID][]*idleWaiter{}, nil
}

// CloseWrites is the records of the tasks the close stopped, as pending, to write back straight to Storage with nothing
// published, as a close is not a crash: the next open counts no restart for them (scheduler.ts join through session.ts
// writeAtClose). A live task that is running when the close ends is one this Harness reserved, since open writes every other
// running task back as pending: its invocation was stopped by the close, or its reservation settled while the close began. It
// reads the records as they stand now, so the write keeps what commits admitted before the seal wrote, such as an abort mark.
func (s *Scheduler) CloseWrites() []*jv.Object {
	var records []*jv.Object
	for _, rec := range s.live.values() {
		if taskStatus(rec) != StatusRunning {
			continue
		}
		cp, _ := getObj(rec, "state").Get("checkpoint")
		records = append(records, withState(rec, obj("status", StatusPending, "checkpoint", cp)))
	}
	slices.SortFunc(records, func(a, b *jv.Object) int { return int(mustInt(a, "id") - mustInt(b, "id")) })
	return records
}

// WriteBack writes the records CloseWrites returned; a *NeedGate error means the host's Storage holds the commit, and the job
// that called it runs again after the answer.
func (s *Scheduler) WriteBack(records []*jv.Object) error { return s.sess.WriteTasks(records) }

// signalInvocations aborts the signal of every running invocation, in task order (scheduler.ts join).
func (s *Scheduler) signalInvocations() {
	ids := make([]ID, 0, len(s.invocations))
	for id := range s.invocations {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		s.invocations[id].signal.Abort()
	}
}

func (s *Scheduler) idle(conversationID ID) bool {
	sc := scopeRoots()
	if conversationID != 0 {
		sc = scopeConv(conversationID)
	}
	// Tasks whose chain is not loaded are not listed below their scope yet; one counts as inside unless its walk up decides
	// otherwise.
	idle := true
	s.unloaded.Range(func(id ID) bool {
		if rec, live := s.live.get(id); live && !getBool(rec, "background") && s.inScope(parentOf(nodeOf(rec)), sc, false) != triFalse {
			idle = false
		}
		return idle
	})
	if !idle {
		return false
	}
	starts := s.roots.Values()
	if conversationID != 0 {
		starts = []ID{conversationID}
	}
	for _, start := range starts {
		s.walkDown(conversationTreeNode(start), walkOptions{}, func(_ ID, n taskNode, rec *jv.Object) bool {
			if rec != nil && !n.background {
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

func (s *Scheduler) settleIdle() {
	defer s.expireContexts()
	for _, id := range slices.Clone(s.idleWaitKeys) {
		if !s.idle(id) {
			continue
		}
		list := s.idleWaiters[id]
		s.dropIdleKey(id)
		for _, w := range list {
			if w.remove != nil {
				w.remove()
			}
			s.loop.Defer(func() { w.done(nil) })
		}
	}
}

type scope struct {
	conversation ID
	roots        bool
}

func scopeConv(id ID) scope { return scope{conversation: id} }
func scopeRoots() scope     { return scope{roots: true} }

type tri uint8

const (
	triFalse tri = iota
	triTrue
	triUnknown
)
