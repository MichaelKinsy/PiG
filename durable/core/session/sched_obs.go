package session

import (
	"errors"
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func (s *Scheduler) scheduleReconcile() {
	// A read-only reconcile could only fail: what it writes is refused.
	if s.reconcileScheduled || s.closing || s.sess.ReadOnly {
		return
	}
	s.reconcileScheduled = true
	s.microtask(s.reconcile)
}

// microtask is `queueMicrotask(fn)` as the scheduler's code calls it. Inside the commit observer (scheduler.ts #observe, which runs at the
// publication, in the Storage's adopt) a microtask runs one tick after the publication: the job #kick queues to drain the reservations
// enqueues its line job there (job 14 of the cold submit, 225 -> 226), and behind it the dispatch to the host's `subscribeCommits` listeners
// (session.ts #notifyHosts) a listener registered at adoption queued first, so a commit such a listener makes is admitted to the line first.
func (s *Scheduler) microtask(fn func()) {
	if s.observing && s.loop.Hops != nil && s.loop.Hops(1, funcResumer(fn)) {
		return
	}
	s.loop.Defer(fn)
}

// abortLater signals the run of inv: scheduler.ts #observe queues `invocation.controller.abort()` as a microtask inside the Storage's adopt, so it
// runs one tick after the publication, behind the dispatch to the host's `subscribeCommits` listeners (queued first, in the same pass) when there
// are any and alone when there are none (no closure: the invocation's own resumer is what the hops chain resumes).
func (s *Scheduler) abortLater(inv *invocation) {
	s.loop.Wait(1, inv.aborter.of(inv))
}

// scheduleReconcileAfterRead is scheduleReconcile for a chain load: Pi's #loadChain schedules it after its Storage read
// resolved, so the microtask runs once the job's reads so far have taken their ticks, not at the job's start.
func (s *Scheduler) scheduleReconcileAfterRead() {
	if s.reconcileScheduled || s.closing || s.sess.ReadOnly {
		return
	}
	if s.env.ReadTicks == nil || s.env.After == nil {
		s.scheduleReconcile()
		return
	}
	n := s.env.ReadTicks()
	if n <= 0 {
		s.scheduleReconcile()
		return
	}
	s.reconcileScheduled = true
	s.env.After(n, s.reconcile)
}

func (s *Scheduler) kick() {
	s.scheduleWake()
	s.dirty = true
	if s.draining || !s.enabled || s.closing {
		return
	}
	s.draining = true
	s.microtask(s.drain)
}

// observe is the scheduler's commit listener (scheduler.ts #observe).
func (s *Scheduler) observe(pub *Publication) {
	s.observing = true
	s.observeCommit(pub)
	s.observing = false
}

func (s *Scheduler) observeCommit(pub *Publication) {
	s.advanceContexts(pub)
	var updated []*jv.Object
	var failed []ID
	changed := false
	// The conversations whose idleness the commit's task changes can change; every conversation when a chain is not known.
	dirty, everything := s.dirtyScopes, false
	clear(dirty)
	clear(s.dirtyTasks)
	// Edges first, so the tasks a commit creates in a new conversation hang from it.
	for _, ch := range pub.Changes {
		if ch.Kind == ChangeTable && ch.Table == WriteConversation {
			cid := mustInt(ch.Record, "id")
			if _, known := s.edges[cid]; !known {
				var owner ID
				if ow := getObj(ch.Record, "owner"); ow != nil {
					owner = mustInt(ow, "taskId")
				}
				s.setEdge(cid, owner)
			}
		}
	}
	for _, ch := range pub.Changes {
		if ch.Kind != ChangeTable || ch.Table != WriteTask {
			continue
		}
		changed = true
		rec := ch.Record
		id := mustInt(rec, "id")
		prev, hadPrev := s.live.get(id)
		if failedOutcome(rec) && (!hadPrev || !failedOutcome(prev)) {
			failed = append(failed, id)
		}
		var before *jv.Object
		if hadPrev {
			before = prev
		}
		s.track(before, rec)
		// Pi 35dfa757 idleness counts live tasks, so only a task that became live or ended can change it.
		// Collected before sweep, which may forget the chain of a task that ended. A new task whose chain is not loaded has its
		// scopes settled once the chain is (loadChain), where Pi has loaded the chain of a new task in the commit that creates it.
		pendingChain := !hadPrev && s.unloaded.Has(id)
		if (!hadPrev || taskStatus(rec) == StatusTerminal) && !everything && !pendingChain && !s.addScopesAbove(rec, dirty, s.dirtyTasks) {
			everything = true
		}
		if taskStatus(rec) == StatusTerminal {
			delete(s.failedMigrations, id)
			delete(s.migrations, id)
			s.failFastChecks = slices.DeleteFunc(s.failFastChecks, func(x ID) bool { return x == id })
			s.resolveTaskWaiters(id, rec)
			s.scheduleReconcile()
			continue
		}
		if getBool(rec, "abortRequested") && (!hadPrev || !getBool(prev, "abortRequested")) {
			s.cascadePending = true
			if inv := s.invocations[id]; inv != nil && !inv.abortMode {
				// scheduler.ts #observe: `queueMicrotask(() => invocation.controller.abort())`, queued inside the Storage's adopt behind the
				// host listeners' dispatch, so the run is signalled after they saw the mark and outside the adoption.
				s.abortLater(inv)
			}
		}
		// A request replacing a restart mark upgrades the marks of the work below.
		if hadPrev && prev.Has("abortReason") && !rec.Has("abortReason") {
			s.cascadePending = true
		}
		status := taskStatus(rec)
		if status == StatusCompleting && (!hadPrev || taskStatus(prev) != StatusCompleting) {
			if cancellationIntent(rec) {
				s.cascadePending = true
			}
			s.scheduleReconcile()
		}
		if status == StatusWaiting {
			if pol, _ := getStr(getObj(rec, "state"), "policy"); pol == "failFast" && (!hadPrev || taskStatus(prev) != StatusWaiting) {
				s.addFailFast(id)
				s.scheduleReconcile()
			}
		}
		// Its owners and place never change: what is above it is checked once, when it becomes live.
		if !hadPrev {
			updated = append(updated, rec)
		}
	}
	for _, id := range failed {
		if waiting := s.waiters[id]; waiting != nil {
			for _, waiter := range waiting.Values() {
				if rec, ok := s.live.get(waiter); ok && taskStatus(rec) == StatusWaiting {
					if pol, _ := getStr(getObj(rec, "state"), "policy"); pol == "failFast" {
						s.addFailFast(waiter)
						s.scheduleReconcile()
					}
				}
			}
		}
	}
	for _, ch := range pub.Changes {
		if ch.Kind != ChangeTable || ch.Table != WriteSubmission {
			continue
		}
		rec := ch.Record
		if st, _ := getStr(rec, "status"); st != "queued" {
			continue
		}
		if typ, _ := getStr(rec, "type"); typ != "input" {
			continue
		}
		// Only an owner with cancellation intent withdraws it; a later intent's mark runs the cascade then.
		if s.intent.Len() == 0 {
			continue
		}
		u := up{id: mustInt(rec, "conversationId")}
		if !s.known(conversationTreeNode(u.id)) || s.belowCancelled(u) {
			s.cascadePending = true
		}
	}
	for _, rec := range updated {
		p := parentOf(nodeOf(rec))
		if s.unloaded.Has(mustInt(rec, "id")) {
			s.scheduleReconcile()
		} else if !getBool(rec, "background") && !getBool(rec, "abortRequested") && s.belowCancelled(p) {
			s.cascadePending = true
		}
	}
	if s.cascadePending {
		s.scheduleReconcile()
	}
	s.sweep()
	if !changed {
		return
	}
	if everything {
		dirty = nil
	}
	s.settleIdle(dirty)
	s.kick()
}

func idsOf(state *jv.Object, key string) []ID {
	list, _ := state.Get(key)
	items, _ := list.([]any)
	out := make([]ID, 0, len(items))
	for _, it := range items {
		if f, ok := it.(float64); ok {
			out = append(out, ID(f))
		}
	}
	return out
}

// reconcile is scheduler.ts #reconcile: one commit that cascades cancellation,
// applies failFast, withdraws queued inputs and finalizes completing tasks.
func (s *Scheduler) reconcile() {
	s.reconcileScheduled = false
	work := s.reconcileWork()
	if work {
		s.reconcilingWork++
	}
	checks := slices.Clone(s.failFastChecks)
	s.failFastChecks = nil
	j := &reconcileJob{s: s, work: work, checks: checks}
	j.rn = j
	s.loop.QueueInternal(&j.job)
}

// reconcileJob is the commit of one reconcile pass queued on the Session line: the line job and the state of its callback in one
// allocation, since a task graph's commits queue one each, thousands at a time.
type reconcileJob struct {
	job
	s *Scheduler
	// cascade is whether the pass loads the queued inputs: read once, when the callback first runs (Pi reads #cascadePending there). A
	// run again after a gate keeps it, as Pi's callback waits in place.
	cascade, started bool
	work             bool
	// taken is set when the callback took the finalize checks into round: the job runs again from its start when a Storage gate or a
	// read answers, so the checks the first run took stay with this reconcile, as Pi's callback runs once and clears them as it takes
	// them (#finalize).
	taken  bool
	checks []ID
	round  []ID
}

// RunJob is the reconcile pass's commit.
func (j *reconcileJob) RunJob() error {
	_, err := j.s.sess.RunCommit(j.change, TxScope{})
	return err
}

// DoneJob continues the pass once its commit is done.
func (j *reconcileJob) DoneJob(err error) {
	s := j.s
	if err != nil {
		s.failSession(err)
	}
	if j.work {
		s.reconcilingWork--
	}
	s.scheduleWake()
	s.settleAfterReconcile()
}

func (j *reconcileJob) change(tx *Tx) error {
	s, checks := j.s, j.checks
	if s.closing {
		return nil
	}
	if !j.started {
		j.cascade, j.started = s.cascadePending, true
	}
	cascade := j.cascade
	before := 0
	if s.env.ReadTicks != nil {
		before = s.env.ReadTicks()
	}
	queued, awaits, err := s.loadScopesAwaiting(cascade)
	if err != nil {
		return err
	}
	// Pi clears #cascadePending once the load returned: a publication while the load waits still finds it set and schedules another pass.
	if cascade {
		s.cascadePending = false
	}
	// The transaction's ticks replace the job's Store reads, so charge the Storage reads #loadScopes made and a tick for
	// each call it awaited (scheduler.ts:680-693).
	loaded := 0
	if s.env.ReadTicks != nil {
		loaded = s.env.ReadTicks() - before
	}
	// `await #loadScopes` also returns once (1); `await #marks()` awaits its region and returns (the region's tick is charged by endRegion, 1 more); the withdrawn
	// inputs' region and `await #finalize(tx)`, whose rounds each await a region, add theirs.
	tx.charge(loaded + awaits + 1)
	s.beginRegion()
	s.record(IntentKey)
	for _, id := range s.intent.Values() {
		s.liveRecord(id)
		s.walkDown(taskTreeNode(id), walkOptions{enter: func(below ID) bool { return !s.intent.Has(below) }}, func(below ID, n taskNode, rec *jv.Object) bool {
			if rec != nil {
				s.liveRecord(below)
			}
			return true
		})
	}
	for _, id := range checks {
		if waiter, ok := s.liveRecord(id); ok && taskStatus(waiter) == StatusWaiting {
			for _, member := range idsOf(getObj(waiter, "state"), "on") {
				s.liveRecord(member)
			}
		}
	}
	if err := s.endRegion(tx); err != nil {
		return err
	}
	tx.charge(1)
	// A cascade from an abandoned owner passes its restart reason on; any other intent marks, or upgrades a restart mark, as
	// a request. Collected first, so a request wins over a restart mark of the same pass.
	marks := abortMarks{byID: map[ID]stagedMark{}}
	// Every live foreground task below a live owner with cancellation intent, which is its nearest one: a walk down from
	// each owner with intent does not enter another, whose own walk covers what is below it. Derived again on every
	// pass, as loading edges can reveal a cancelled owner.
	for _, id := range s.intent.Values() {
		owner, _ := s.live.get(id)
		// Only an owner abandoned after a restart, and nothing else, passes its reason on.
		reason := ""
		if abortReasonOf(owner) == "restart" && !failedOutcome(owner) {
			reason = "restart"
		}
		s.walkDown(taskTreeNode(id), walkOptions{enter: func(below ID) bool { return !s.intent.Has(below) }}, func(_ ID, n taskNode, rec *jv.Object) bool {
			if rec != nil && !n.background {
				marks.add(rec, reason)
			}
			return true
		})
	}
	for _, id := range checks {
		waiter, ok := s.live.get(id)
		if !ok || taskStatus(waiter) != StatusWaiting {
			continue
		}
		on := idsOf(getObj(waiter, "state"), "on")
		any, err := s.anyFailed(on)
		if err != nil {
			return err
		}
		if !any {
			continue
		}
		for _, member := range on {
			if rec, ok := s.live.get(member); ok && !failedOutcome(rec) {
				marks.add(rec, "")
			}
		}
	}
	for _, id := range marks.order {
		if err := tx.stageTask(withAbortMark(marks.byID[id].rec, marks.byID[id].reason)); err != nil {
			return err
		}
	}
	s.beginRegion()
	for _, id := range queued {
		s.belowCancelled(up{id: id})
	}
	if err := s.endRegion(tx); err != nil {
		return err
	}
	for _, id := range queued {
		if s.belowCancelled(up{id: id}) {
			if err := s.env.WithdrawInputs(tx, id); err != nil {
				return err
			}
		}
	}
	if !j.taken {
		j.round, j.taken = s.finalizeChecks.Values(), true
		s.finalizeChecks.Clear()
	}
	return s.finalize(tx, j.round)
}

// stagedMark is the record a reconcile pass marks for abort, and the restart reason it passes on.
type stagedMark struct {
	rec    *jv.Object
	reason string
}

// abortMarks collects the tasks a reconcile pass marks, in the order they were found.
type abortMarks struct {
	byID  map[ID]stagedMark
	order []ID
}

// add marks rec: a request wins over a restart mark of the same pass.
func (m *abortMarks) add(rec *jv.Object, reason string) {
	id := mustInt(rec, "id")
	found, ok := m.byID[id]
	if ok && found.reason == "" {
		return
	}
	if getBool(rec, "abortRequested") && (!rec.Has("abortReason") || reason != "") {
		return
	}
	if !ok {
		m.order = append(m.order, id)
	}
	m.byID[id] = stagedMark{rec, reason}
}

// reconcileWork reports whether a reconcile pass would have work: a cascade to derive, held outcomes to check, or failFast
// waits (scheduler.ts #reconcileWork).
func (s *Scheduler) reconcileWork() bool {
	return s.cascadePending || s.finalizeChecks.Len() > 0 || len(s.failFastChecks) > 0
}

func (s *Scheduler) anyFailed(ids []ID) (bool, error) {
	for _, id := range ids {
		rec, ok := s.live.get(id)
		if !ok {
			var err error
			if rec, err = s.sess.store.Task(id); err != nil {
				return false, err
			}
		}
		if rec != nil && failedOutcome(rec) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Scheduler) finalize(tx *Tx, round []ID) error {
	round = slices.Clone(round)
	done := map[ID]bool{}
	o := overlayOf(tx)
	// `await #finalize` returns once more after its last region.
	defer tx.charge(1)
	for {
		// Each round is one decision region.
		s.beginRegion()
		for _, id := range round {
			rec, ok := o.tasks[id]
			if !ok {
				rec, ok = s.liveRecord(id)
			}
			if ok && taskStatus(rec) == StatusCompleting && !done[id] {
				s.hasOwnedLive(id, o, done)
			}
		}
		if err := s.endRegion(tx); err != nil {
			return err
		}
		if len(round) == 0 {
			return nil
		}
		next := newOrderedSet[ID]()
		for _, id := range round {
			rec, ok := o.tasks[id]
			if !ok {
				rec, ok = s.live.get(id)
			}
			if !ok || taskStatus(rec) != StatusCompleting || done[id] {
				continue
			}
			if s.hasOwnedLive(id, o, done) {
				continue
			}
			outcome := getObj(getObj(rec, "state"), "outcome")
			staged, err := tx.setTask(withState(rec, obj("status", StatusTerminal, "outcome", outcome)), false, true)
			if err != nil {
				return err
			}
			if _, listed := o.tasks[id]; !listed {
				o.order = append(o.order, id)
			}
			o.tasks[id] = staged
			done[id] = true
			if os, _ := getStr(outcome, "status"); os == "faulted" || os == "orphaned" {
				if err := s.env.SettleOutcome(tx, rec, outcome); err != nil {
					return err
				}
			}
			if above := s.nearestLiveAbove(nodeOf(rec), o); above != nil && taskStatus(above) == StatusCompleting {
				next.Add(mustInt(above, "id"))
			}
		}
		round = next.Values()
		if len(round) == 0 {
			return nil
		}
	}
}

// drain reserves and starts every runnable task until nothing is dirty.
func (s *Scheduler) drain() {
	d := &drainer{s: s}
	d.loop()
}

// drainer is one drain: its passes run one after the other, so the pass's state stays in it.
type drainer struct {
	s   *Scheduler
	res []*reservation
	// again: the next Resume is the pass that follows an abort handler's start, not the start of res.
	again bool
}

func (d *drainer) finish() {
	s := d.s
	s.draining = false
	if s.dirty {
		s.kick()
	}
}

func (d *drainer) loop() {
	s := d.s
	if !s.dirty || !s.enabled || s.closing {
		d.finish()
		return
	}
	s.dirty = false
	s.reserve(d.reserved)
}

func (d *drainer) reserved(res []*reservation, err error) {
	if err != nil {
		d.s.failSession(err)
		d.finish()
		return
	}
	// `for (const reservation of await this.#reserve())` (scheduler.ts #drain): #reserve is an async function, so #drain resumes a tick after it
	// returns, and then starts each reservation and asks for the next pass.
	d.res = res
	d.s.loop.NextTickResumer(d)
}

// Resume starts the reservations of the pass and asks for the next one.
func (d *drainer) Resume() {
	if d.again {
		d.again = false
		d.loop()
		return
	}
	aborting := false
	for _, r := range d.res {
		d.s.start(r)
		aborting = aborting || r.inv.abortMode
	}
	d.res = nil
	if aborting {
		// Pi starts an abort handler in this tick, synchronously: a commit it makes first reaches the line before the next pass. The host runs the
		// handler after this step, so the pass follows a hop later.
		d.again = true
		d.s.loop.NextTickResumer(d)
		return
	}
	d.loop()
}

func (s *Scheduler) reserve(then func([]*reservation, error)) {
	var reservations []*reservation
	var abandoned []ID
	var faulted []faultedTask
	cleanup := func() {
		for _, r := range reservations {
			delete(s.invocations, r.inv.taskID)
			s.runnable.Add(r.inv.taskID)
			r.inv.finish()
		}
		reservations = nil
	}
	s.commitInternal(func(tx *Tx) error {
		cleanup()
		abandoned = nil
		faulted = nil
		// The job runs again from its start when a Storage gate or a read answers; Pi's callback runs once and decides at its start
		// (scheduler.ts #reserve), so a close that sealed meanwhile does not undo a reservation the first run made.
		if s.reserveSkip == pinUnset {
			s.reserveSkip = pinRun
			if !s.enabled || s.closing {
				s.reserveSkip = pinSkip
			}
		}
		if s.reserveSkip == pinSkip {
			return nil
		}
		if s.abandoned.Len() > 0 {
			s.beginRegion()
			for _, id := range s.abandoned.Values() {
				s.liveRecord(id)
			}
			if err := s.endRegionPlain(tx); err != nil {
				return err
			}
		}
		for _, id := range s.abandoned.Values() {
			if rec, live := s.live.get(id); live && !getBool(rec, "abortRequested") {
				if err := tx.stageTask(withAbortMark(rec, "restart")); err != nil {
					return err
				}
			}
		}
		if s.abandoned.Len() > 0 {
			abandoned = s.abandoned.Values()
			return nil
		}
		before := 0
		if s.env.ReadTicks != nil {
			before = s.env.ReadTicks()
		}
		_, awaits, err := s.loadScopesAwaiting(false)
		if err != nil {
			return err
		}
		// Every pass picks up sleeps whose time has come: a pass started by any change may be the first to run after a deadline,
		// and the wake it schedules no longer keeps a timer for a due sleep.
		s.wakeDue()
		// Pi's #reserve callback awaits only #loadScopes(false) (scheduler.ts #reserve): one tick, plus the Storage reads of
		// the chain loads and a tick for each #loadChain call it awaited (scheduler.ts:680-684).
		ticks := 1 + awaits
		if s.env.ReadTicks != nil {
			ticks += s.env.ReadTicks() - before
		}
		// The candidates are scanned in a decision region, one tick of its own.
		s.beginRegion()
		for _, id := range s.runnable.Values() {
			if s.invocations[id] != nil {
				continue
			}
			if rec, live := s.liveRecord(id); live && taskStatus(rec) != StatusCompleting {
				s.waitingOn(rec, true)
				if !getBool(rec, "abortRequested") && !getBool(rec, "background") {
					s.belowCancelled(parentOf(nodeOf(rec)))
				}
			}
		}
		if err := s.endRegion(tx); err != nil {
			return err
		}
		// The pass is Pi 1.5.1's `await this.#planCandidates()` (scheduler.ts), an async function around the decision region: the region's
		// tick, then the async function's return, one tick more than the `await this.#region(...)` the pass was before.
		ticks += 2
		defer func() { tx.ticks = ticks }()
		var snapshot Snapshot
		for _, id := range s.runnable.Values() {
			rec, live := s.live.get(id)
			if !live || taskStatus(rec) == StatusCompleting || s.invocations[id] != nil {
				s.runnable.Delete(id)
				continue
			}
			// It returns once what it waits for ends, its record changes, or the work below it ends.
			if len(s.waitingOn(rec, true)) > 0 {
				s.runnable.Delete(id)
				continue
			}
			// It returns at the first pass after its time comes, which the timer, Resume, or any change starts.
			if s.notDue(rec) {
				s.runnable.Delete(id)
				continue
			}
			abortMode := getBool(rec, "abortRequested")
			// Work below an owner with cancellation intent waits for its cascade mark instead of running a phase; schedule the
			// cascade, which may not have run yet.
			if !abortMode && !getBool(rec, "background") && s.belowCancelled(parentOf(nodeOf(rec))) {
				s.cascadePending = true
				s.scheduleReconcile()
				continue
			}
			// Work cut off restartLimit times in a row would run forever: an abort handler, or a phase of a task that does not
			// end itself at the limit, ends the task faulted instead. The settings are read only for a task that was cut off
			// at all.
			if restartsOf(rec) > 0 && restartsOf(rec) >= s.restartLimit() && (abortMode || !s.limitsRestarts(rec)) {
				kind, _ := getStr(rec, "kind")
				message := sprintf("Task %d (%s) was cut off %d times without progress", id, kind, restartsOf(rec))
				if abortMode {
					message = sprintf("Abort handler of task %d (%s) was cut off %d times", id, kind, restartsOf(rec))
				}
				if err := s.terminate(tx, rec, obj("status", "faulted", "error", obj("message", message, "detail", obj("reason", "restart_limit")))); err != nil {
					return err
				}
				faulted = append(faulted, faultedTask{rec: rec, message: message})
				continue
			}
			if snapshot == nil {
				snapshot = s.env.Snapshot()
			}
			res := s.resolve(rec, snapshot)
			if res.pending {
				continue
			}
			if res.blocked != "" {
				// An abandoned task waits for its definition, so its abort handler can clean up.
				if abortMode && !rec.Has("abortReason") {
					if err := s.terminate(tx, rec, obj("status", "orphaned", "reason", res.blocked)); err != nil {
						return err
					}
				}
				continue
			}
			state := getObj(res.record, "state")
			if res.record != rec || taskStatus(rec) != StatusRunning {
				cp, _ := state.Get("checkpoint")
				if err := tx.stageFresh(withState(res.record, obj("status", StatusRunning, "checkpoint", cp))); err != nil {
					return err
				}
			}
			// Registered on the line, so marks and later reservations see it and close joins it; endInvocation makes it a
			// candidate again.
			s.runnable.Delete(id)
			inv := s.createInvocation(rec, abortMode, res.task.Extension)
			reservations = append(reservations, &reservation{inv: inv, task: res.task, snapshot: snapshot})
		}
		return nil
	}, TxScope{}, func(err error) {
		s.reserveSkip = pinUnset
		if err != nil {
			cleanup()
			then(nil, err)
			return
		}
		for _, f := range faulted {
			info := s.aboutRecord("task_faulted", f.rec)
			info.Set("reason", "restart_limit")
			s.report(errors.New(f.message), info)
		}
		// Marked, or already marked or gone: done with them. Reserve again, now seeing the marks.
		for _, id := range abandoned {
			s.abandoned.Delete(id)
		}
		if len(abandoned) > 0 {
			s.dirty = true
		}
		then(reservations, nil)
	})
}

func (s *Scheduler) waitingOn(rec *jv.Object, first bool) []ID {
	if getBool(rec, "abortRequested") {
		return s.ownedLiveOf(mustInt(rec, "id"), nil, first, nil)
	}
	if taskStatus(rec) != StatusWaiting {
		return nil
	}
	var out []ID
	for _, id := range idsOf(getObj(rec, "state"), "on") {
		if _, ok := s.live.get(id); ok {
			out = append(out, id)
			if first {
				break
			}
		}
	}
	return out
}

func missingMigration(rec *jv.Object, def *TaskImpl) error {
	kind, _ := getStr(rec, "kind")
	return errors.New("Task " + kind + " version " + jv.FormatNumber(float64(def.Def.Version)) + " has no migration from " + jv.FormatNumber(float64(mustInt(rec, "version"))))
}

// resolve is scheduler.ts #resolve: the definition and the (possibly migrated) record, or why the task is blocked.
func (s *Scheduler) resolve(rec *jv.Object, snapshot Snapshot) resolution {
	kind, _ := getStr(rec, "kind")
	task, reason, migrates := s.fit(rec, snapshot.Task(kind))
	if reason != "" {
		return resolution{blocked: reason}
	}
	if !migrates {
		return resolution{task: task, record: rec}
	}
	if task.MigrateAsync != nil {
		return s.resolveHostMigration(rec, task)
	}
	if task.Migrate == nil {
		err := missingMigration(rec, task)
		s.migrationFailed(rec, task, err)
		return resolution{blocked: "migration_failed"}
	}
	input, _ := rec.Get("input")
	state := getObj(rec, "state")
	cp, _ := state.Get("checkpoint")
	newInput, newCP, err := task.Migrate(input, cp, int(mustInt(rec, "version")))
	if err != nil {
		s.migrationFailed(rec, task, err)
		return resolution{blocked: "migration_failed"}
	}
	nextState := with(state, "checkpoint", jv.Clone(newCP))
	migrated := with(rec, "version", float64(task.Def.Version), "input", jv.Clone(newInput), "state", nextState)
	return resolution{task: task, record: migrated}
}

// resolveHostMigration asks the host to migrate once and answers pending until it has: the answer applies like Migrate's.
func (s *Scheduler) resolveHostMigration(rec *jv.Object, task *TaskImpl) resolution {
	id := mustInt(rec, "id")
	from := int(mustInt(rec, "version"))
	m := s.migrations[id]
	if m == nil || m.task != task || m.from != from {
		m = &migration{task: task, from: from}
		s.migrations[id] = m
		input, _ := rec.Get("input")
		cp, _ := getObj(rec, "state").Get("checkpoint")
		task.MigrateAsync(input, cp, from, func(newInput, newCP any, err error) {
			m.done, m.input, m.cp, m.err = true, newInput, newCP, err
			s.kick()
		})
	}
	if !m.done {
		return resolution{pending: true}
	}
	delete(s.migrations, id)
	if m.err != nil {
		s.migrationFailed(rec, task, m.err)
		return resolution{blocked: "migration_failed"}
	}
	nextState := with(getObj(rec, "state"), "checkpoint", jv.Clone(m.cp))
	migrated := with(rec, "version", float64(task.Def.Version), "input", jv.Clone(m.input), "state", nextState)
	return resolution{task: task, record: migrated}
}

// migrationFailed records a failed migration: the task is blocked now without a commit, so the graph and the host's wake must
// hear it, and the failure is reported with the extension that installed the definition (scheduler.ts #resolve).
func (s *Scheduler) migrationFailed(rec *jv.Object, task *TaskImpl, err error) {
	s.failedMigrations[mustInt(rec, "id")] = failedMigration{task, err}
	s.loop.Defer(s.notifyBlocked)
	s.scheduleWake()
	info := s.aboutRecord("migration_failed", rec)
	info.Delete("extension")
	if task.Extension != "" {
		info.Set("extension", task.Extension)
	}
	s.report(err, info)
}

func (s *Scheduler) fit(rec *jv.Object, task *TaskImpl) (*TaskImpl, string, bool) {
	if task == nil {
		return nil, "missing_task", false
	}
	version := task.Def.Version
	recVersion := int(mustInt(rec, "version"))
	if version == recVersion {
		return task, "", false
	}
	if version < recVersion {
		return nil, "task_too_old", false
	}
	if failed, ok := s.failedMigrations[mustInt(rec, "id")]; ok && failed.task == task {
		return nil, "migration_failed", false
	}
	return task, "", true
}

func canReserve(task *TaskImpl, rec *jv.Object) bool {
	v := int(mustInt(rec, "version"))
	return task.Def.Version == v || (task.Def.Version > v && (task.Migrate != nil || task.MigrateAsync != nil))
}

// faultedTask is an abort handler that was cut off restartLimit times: its task ended faulted in the pass.
type faultedTask struct {
	rec     *jv.Object
	message string
}

func (s *Scheduler) createInvocation(rec *jv.Object, abortMode bool, extension string) *invocation {
	kind, _ := getStr(rec, "kind")
	inv := &invocation{taskID: mustInt(rec, "id"), kind: kind, extension: extension, conversationID: mustInt(rec, "conversationId"), abortMode: abortMode, loop: s.loop, record: rec}
	if _, started := rec.Get("startedAt"); !started && s.env.Telemetry != nil {
		inv.telemetry().links, _ = rec.Get("links")
	}
	inv.s = s
	s.invocations[inv.taskID] = inv
	return inv
}

func (s *Scheduler) endInvocation(inv *invocation) {
	if inv.ended {
		return
	}
	inv.ended = true
	if s.invocations[inv.taskID] == inv {
		delete(s.invocations, inv.taskID)
		if _, live := s.live.get(inv.taskID); live {
			s.runnable.Add(inv.taskID)
		}
	}
	for _, stop := range inv.watches {
		stop()
	}
	inv.signal.AbortWith(endedError(inv))
}
