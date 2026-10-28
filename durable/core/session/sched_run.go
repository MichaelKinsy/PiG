package session

import (
	"math"
	"slices"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

type phaseResult struct {
	checkpoint any
	failure    error
}

// decision is the outcome of a step's decide: continue, stop, or fault the task.
type decision struct {
	kind  uint8 // decideContinue, decideStop, decideFault
	fault error
}

const (
	decideContinue uint8 = iota + 1
	decideStop
	decideFault
)

// phaseSpanHops is the ticks the span of a task phase (scheduler.ts #phase: `createTypedSpanStarter(...)("pi.durable.phase", ...)`, Pi 2ec55d80
// "Durable and pi-ai telemetry") adds after the phase's decision step resolves, before the invocation goes on: `run` returns the step's promise
// from an async arrow (an adoption), the span callback awaits it, and the no-op span's `startSpan` is an async function that returns the callback's
// promise (another adoption) which `#run` awaits. Measured on Pi 6c384aa3 over F-ticks-tools-gate (the commit that follows a phase's decision step comes 3 ticks later, and the 4th tick of the model call is runtime.commit's return).
const phaseSpanHops = 3

func (s *Scheduler) start(r *reservation) {
	inv := r.inv
	finish := func() {
		s.endInvocation(inv)
		inv.finish()
		s.kick()
	}
	if inv.abortMode {
		s.runAbort(r, finish)
	} else {
		s.run(r, finish)
	}
}

type runState struct {
	task     *TaskImpl
	snapshot Snapshot
	reported *TaskImpl
	hasRep   bool
	inv      *invocation
}

// runner is the run of one task's phases: what the scheduler keeps of an invocation between its steps, in one allocation (a task
// graph reserves thousands of invocations at once), and the step's two continuations.
type runner struct {
	s   *Scheduler
	inv *invocation
	st  runState
	rt  Runtime
	// previous is the result of the phase that ran before the step in flight, when hasPrevious; cp is the checkpoint the running
	// phase started from.
	previous    phaseResult
	hasPrevious bool
	cp          any
	current     *jv.Object
	finish      func()
}

func (s *Scheduler) run(r *reservation, finish func()) {
	rn := &runner{s: s, inv: r.inv, st: runState{task: r.task, snapshot: r.snapshot, inv: r.inv}, finish: finish}
	rn.rt = Runtime{s: s, inv: r.inv, snap: &rn.st.snapshot}
	rn.next()
}

// previousResult is the phase result the step in flight decides on: nil before the first phase.
func (rn *runner) previousResult() *phaseResult {
	if !rn.hasPrevious {
		return nil
	}
	return &rn.previous
}

// next queues the step that follows the phase that just ended.
func (rn *runner) next() { rn.s.step(rn.inv, rn) }

func (rn *runner) decide(tx *Tx, current *jv.Object) (decision, error) {
	return rn.s.decide(tx, current, rn.previousResult(), &rn.st, rn.inv.extension)
}

// after continues the run once the step has committed: it starts the next phase, or ends the invocation.
func (rn *runner) after(current *jv.Object) {
	s, inv := rn.s, rn.inv
	ran := rn.hasPrevious
	// #run awaits #phase, which wraps the phase handler and the decision step that follows it in a span: it ends with the step.
	if ran {
		s.endPhase(inv)
	}
	if current == nil || s.closing {
		inv.afterSpan(ran, rn.finish, false)
		return
	}
	// #run awaits #step, an async function that awaited the step's commit: the phase starts a tick after the job resolved
	// (scheduler.ts :919-926, :1000-1002).
	// The phase runs inside its span: a Harness with telemetry asks the host for it first (afterSpan, spanFirst), which runs the phase.
	rn.current = current
	inv.spanFirst = s.env.Telemetry != nil
	inv.afterSpan(ran, rn.startPhase, true)
}

// startPhase runs the phase of the checkpoint the step committed.
func (rn *runner) startPhase() {
	s, inv, current := rn.s, rn.inv, rn.current
	if inv.spanAsk {
		// The host's span comes first; its answer runs this again for the phase.
		inv.spanAsk = false
		s.startPhase(inv)
		return
	}
	rn.current = nil
	rn.cp, _ = getObj(current, "state").Get("checkpoint")
	rn.rt.agentReset()
	rn.st.task.Phase(&rn.rt, current, rn.phaseDone)
}

// phaseDone is the continuation of a phase: its result goes to the next step.
func (rn *runner) phaseDone(err error) {
	rn.previous, rn.hasPrevious = phaseResult{checkpoint: rn.cp, failure: err}, true
	rn.cp = nil
	rn.s.loop.Defer(rn.next)
}

func (s *Scheduler) decide(tx *Tx, current *jv.Object, previous *phaseResult, st *runState, extension string) (decision, error) {
	if getBool(current, "abortRequested") {
		return decision{kind: decideStop}, nil
	}
	// An owner's cancellation intent ends it too, before its cascade marks it; reservation then holds it back.
	if !getBool(current, "background") && s.belowCancelled(parentOf(nodeOf(current))) {
		s.cascadePending = true
		s.scheduleReconcile()
		return decision{kind: decideStop}, nil
	}
	if previous == nil {
		return decision{kind: decideContinue}, nil
	}
	if previous.failure != nil {
		return decision{kind: decideFault, fault: previous.failure}, nil
	}
	cp, _ := getObj(current, "state").Get("checkpoint")
	if jv.Equal(cp, previous.checkpoint) {
		kind, _ := getStr(current, "kind")
		phase := ""
		if o, ok := previous.checkpoint.(*jv.Object); ok {
			phase, _ = getStr(o, "phase")
		}
		return decision{kind: decideFault, fault: errorf("Task %s phase %s returned without durable progress", kind, phase)}, nil
	}
	snapshot, err := s.freshSnapshot()
	if err != nil {
		return decision{}, err
	}
	st.snapshot = snapshot
	kind, _ := getStr(current, "kind")
	next := st.snapshot.Task(kind)
	if next != st.task {
		if next != nil && canReserve(next, current) {
			cpv, _ := getObj(current, "state").Get("checkpoint")
			_ = tx.stageTask(with(current, "state", obj("status", StatusPending, "checkpoint", cpv)))
			return decision{kind: decideStop}, nil
		}
		if !st.hasRep || st.reported != next {
			st.reported, st.hasRep = next, true
			reason := "incompatible_task"
			if next == nil {
				reason = "missing_task"
			}
			info := s.aboutRecord("task_outdated", current)
			info.Delete("extension")
			if extension != "" {
				info.Set("extension", extension)
			}
			info.Set("reason", reason)
			s.reportAt(errorf("Task %d keeps running under its old %s definition", mustInt(current, "id"), kind), info, st.inv.span)
		}
	}
	return decision{kind: decideContinue}, nil
}

// freshSnapshot is registry.snapshot() inside a step job (Env.FreshSnapshot).
func (s *Scheduler) freshSnapshot() (Snapshot, error) {
	if s.env.FreshSnapshot != nil {
		return s.env.FreshSnapshot()
	}
	return s.env.Snapshot(), nil
}

func (s *Scheduler) runAbort(r *reservation, finish func()) {
	inv := r.inv
	current, ok := s.live.get(inv.taskID)
	if !ok || s.closing {
		finish()
		return
	}
	rt := s.newRuntime(inv, &r.snapshot)
	after := func(failure error) {
		s.step(inv, &abortStep{s: s, inv: inv, failure: failure, finish: finish})
	}
	// #runAbort wraps the abort handler and its step in a span of kind abort: the host starts it, which runs the handler.
	if s.env.Telemetry != nil {
		inv.telemetry().abort = abortRun{r, rt, current, after}
		s.startPhase(inv)
		return
	}
	s.abortHandler(r, rt, current, after)
}

// abortStep is the step that follows an abort handler: it faults the task unless the handler left it terminal.
type abortStep struct {
	s       *Scheduler
	inv     *invocation
	failure error
	finish  func()
}

func (a *abortStep) decide(*Tx, *jv.Object) (decision, error) {
	if a.failure != nil {
		return decision{kind: decideFault, fault: a.failure}, nil
	}
	return decision{kind: decideFault, fault: errorf("Abort handler of task %d returned without a terminal outcome", a.inv.taskID)}, nil
}

func (a *abortStep) after(*jv.Object) {
	a.s.endPhase(a.inv)
	a.inv.afterSpan(true, a.finish, false)
}

// abortRun is an abort handler waiting for the host's span.
type abortRun struct {
	r       *reservation
	rt      *Runtime
	current *jv.Object
	after   func(error)
}

// abortHandler runs the abort handler of a reservation; after is what follows it (its step).
func (s *Scheduler) abortHandler(r *reservation, rt *Runtime, current *jv.Object, after func(error)) {
	r.task.Abort(rt, current, func(err error) {
		if n := r.task.AbortReturnHops; n > 0 && s.env.After != nil {
			s.env.After(n, func() { after(err) })
			return
		}
		s.loop.Defer(func() { after(err) })
	})
}

// step runs one reservation step on the line: decide, then continue with the
// running task, or end the invocation (faulting the task when decided).
func (s *Scheduler) step(inv *invocation, st stepper) {
	j := &stepJob{s: s, inv: inv, st: st, span: inv.span, trace: inv.carrier()}
	j.rn = j
	s.loop.QueueAfterTail(&j.job)
}

// stepper is what a step decides and does next: decide runs inside the step's commit, and after once the commit is done (with the
// running task's record, or nil when the invocation ended).
type stepper interface {
	decide(tx *Tx, current *jv.Object) (decision, error)
	after(current *jv.Object)
}

// stepJob is a reservation step queued on the Session line (step): the line job, its commit callback, its report and its
// continuation in one allocation, since every task of a large graph has one queued at once.
type stepJob struct {
	job
	s   *Scheduler
	inv *invocation
	st  stepper
	// span and trace are the commit's Context as the step was queued.
	span   uint32
	trace  any
	result *jv.Object
	fault  error
}

func (j *stepJob) runJob() error {
	_, err := j.s.sess.RunCommit(j.change, TxScope{Span: j.span, Trace: j.trace})
	if err == nil {
		j.s.loop.OnComplete(j.reportFault)
	}
	return err
}

// reportFault reports the fault as the step resumes from its commit, after its publication and before the line's next job.
func (j *stepJob) reportFault() {
	if j.fault != nil {
		info := aboutInvocation("task_faulted", j.inv)
		info.Set("reason", "threw")
		j.s.reportAt(j.fault, info, j.inv.span)
	}
}

func (j *stepJob) change(tx *Tx) error {
	s, inv := j.s, j.inv
	j.result = nil
	j.fault = nil
	found, ok := s.live.get(inv.taskID)
	var current *jv.Object
	if ok && taskStatus(found) == StatusRunning {
		current = found
	}
	d := decision{kind: decideStop}
	if current != nil && !s.closing {
		var err error
		if d, err = j.st.decide(tx, current); err != nil {
			return err
		}
	}
	if d.kind == decideContinue {
		j.result = current
		return nil
	}
	s.endInvocation(inv)
	if d.kind == decideFault {
		j.fault = d.fault
		msg := faultText(d.fault)
		// invTel.fault: the outcome of a task that threw, which the phase's span reports once the task has left the live index.
		outcome := obj("status", "faulted", "error", obj("message", msg))
		if s.env.Telemetry != nil {
			inv.telemetry().fault = outcome
		}
		return s.terminate(tx, current, outcome)
	}
	return nil
}

func (j *stepJob) doneJob(err error) {
	if err != nil {
		j.s.endInvocation(j.inv)
		j.s.failSession(err)
		j.st.after(nil)
		return
	}
	j.st.after(j.result)
}

// terminate settles a task with a scheduler-written outcome: completing while
// owned work is live, else terminal with the harness settlement.
func (s *Scheduler) terminate(tx *Tx, rec *jv.Object, outcome *jv.Object) error {
	if _, err := s.loadScopes(false); err != nil {
		return err
	}
	if s.hasOwnedLive(mustInt(rec, "id"), overlayOf(tx), nil) {
		return tx.stageFresh(withState(rec, obj("status", StatusCompleting, "outcome", outcome)))
	}
	if err := tx.stageFresh(withState(rec, obj("status", StatusTerminal, "outcome", outcome))); err != nil {
		return err
	}
	return s.env.SettleOutcome(tx, rec, outcome)
}

// commitState stages the next state a task commits for itself.
func (s *Scheduler) commitState(tx *Tx, inv *invocation, current *jv.Object, next *jv.Object) error {
	status, _ := getStr(next, "status")
	if status == StatusWaiting {
		if err := s.validateWait(tx, inv, current, idsOf(next, "on"), next); err != nil {
			return err
		}
	}
	if status == StatusSleeping {
		if err := validateSleep(inv, mustInt(current, "id"), next); err != nil {
			return err
		}
	}
	if status == StatusTerminal {
		o := overlayOf(tx)
		if _, err := s.loadScopes(false); err != nil {
			return err
		}
		for _, id := range o.order {
			if err := s.loadChain(parentOf(nodeOf(o.tasks[id])), o); err != nil {
				return err
			}
		}
		if s.hasOwnedLive(mustInt(current, "id"), o, nil) {
			outcome, _ := next.Get("outcome")
			outcome = shared(&s.sess.lastOutcome, outcome)
			return tx.stageFresh(withState(progressed(current), obj("status", StatusCompleting, "outcome", outcome)))
		}
	}
	// A state the task committed that differs from its current one is progress: its restart count starts over. The same
	// checkpoint committed again, for example as intent before an effect, is not.
	if jv.Equal(getObj(current, "state"), next) {
		return tx.stageFresh(withState(current, next))
	}
	return tx.stageFresh(withState(progressed(current), next))
}

// validateWait checks that a wait names existing tasks, and failFast only tasks the waiter owns. It may not close a cycle
// in the wait-for graph, whose edges run from each task to what it waits on and to its live ordinary owned work (spec
// §5.5); waiting on itself or on an owner holding it are the shortest such cycles. An abort handler cannot wait
// (scheduler.ts #validateWait).
//
// Invariant: the wait-for graph is acyclic. Only a wait adds an edge between existing tasks, as a new task has only
// incoming edges, so checking each wait maintains it.
func (s *Scheduler) validateWait(tx *Tx, inv *invocation, current *jv.Object, on []ID, next *jv.Object) error {
	id := mustInt(current, "id")
	if inv.abortMode {
		return errorf("Abort handler of task %d cannot wait", id)
	}
	o := overlayOf(tx)
	policy, _ := getStr(next, "policy")
	var targets []ID
	for _, m := range on {
		member := o.tasks[m]
		if member == nil {
			member, _ = s.live.get(m)
		}
		if member == nil {
			var err error
			if member, err = s.sess.store.Task(m); err != nil {
				return err
			}
		}
		if member == nil {
			return errorf("Task %d does not exist", m)
		}
		owner, _ := getInt(member, "owner")
		ownedByCurrent := owner == id
		if policy == "failFast" && !ownedByCurrent {
			return errorf("Task %d can wait failFast only on tasks it owns; %d is not one", id, m)
		}
		// A live child already holds the waiter (task-owned tasks are never background): waiting on it adds no edge. A
		// terminal task has no edges.
		if taskStatus(member) != StatusTerminal && !ownedByCurrent {
			targets = append(targets, m)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	cycle, err := s.waitCycle(id, targets, o)
	if err != nil {
		return err
	}
	if cycle != nil {
		steps := strconv.AppendInt(nil, id, 10)
		for _, step := range cycle {
			steps = strconv.AppendInt(append(steps, " -> "...), step, 10)
		}
		return errorf("Task %d cannot wait on %d: %s is a wait cycle", id, cycle[0], string(steps))
	}
	return nil
}

// waitCycle returns the path from a member of on back to task in the wait-for graph, if the wait would close a cycle
// (spec §5.1; scheduler.ts #waitCycle). An edge X -> Y means X cannot settle before Y: Y is in the on of waiting X that is
// not abort-marked, or Y is live ordinary owned work of X, which holds X as completing. Each task is visited once and each
// subtree walked once, so a check is bounded by the live work it reaches.
func (s *Scheduler) waitCycle(task ID, on []ID, o *overlay) ([]ID, error) {
	// Owned work is found by walks down, which need the chains of every live task loaded.
	if _, err := s.loadScopes(false); err != nil {
		return nil, err
	}
	// from maps each reached task to the task whose edge reached it; the members of on map to 0, which no task has.
	from := map[ID]ID{}
	walked := map[ID]bool{}
	var pending []ID
	reach := func(id, via ID) {
		if _, seen := from[id]; seen {
			return
		}
		from[id] = via
		pending = append(pending, id)
	}
	for _, id := range on {
		reach(id, 0)
	}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if id == task {
			var path []ID
			for at := id; at != 0; at = from[at] {
				path = append(path, at)
			}
			slices.Reverse(path)
			return path, nil
		}
		record := o.tasks[id]
		if record == nil {
			record, _ = s.live.get(id)
		}
		if record == nil || taskStatus(record) == StatusTerminal {
			continue
		}
		// An abort-marked task waits only for its owned work, as abort runs bottom-up (waitingOn).
		if taskStatus(record) == StatusWaiting && !getBool(record, "abortRequested") {
			for _, member := range idsOf(getObj(record, "state"), "on") {
				reach(member, id)
			}
		}
		if walked[id] {
			continue
		}
		walked[id] = true
		// Below a walked task the walk does not go again: its own walk covered what lies below.
		var found []ID
		opts := walkOptions{o: o, enter: func(below ID) bool { return !walked[below] }}
		s.walkDown(taskTreeNode(id), opts, func(below ID, n taskNode, live *jv.Object) bool {
			if live == nil || n.background {
				return true
			}
			found = append(found, below)
			reach(below, id)
			return true
		})
		for _, below := range found {
			walked[below] = true
		}
	}
	return nil, nil
}

func endedError(inv *invocation) error {
	return errorf("Task %d invocation has ended", inv.taskID)
}

// Read-only: assigned once at initialization.
var _ = slices.Clone[[]int]

// faultText is the message of the error a phase threw (Error.message): an error from host code that crossed the boundary
// as {name, message} reports its message alone.
func faultText(err error) string {
	if t, ok := err.(interface{ Text() string }); ok {
		return t.Text()
	}
	return err.Error()
}

// validateSleep checks that a sleep's until is a time on the Harness clock, and that an abort handler does not sleep
// (scheduler.ts validateSleep).
func validateSleep(inv *invocation, task ID, next *jv.Object) error {
	if inv.abortMode {
		return errorf("Abort handler of task %d cannot sleep", task)
	}
	v, _ := next.Get("until")
	until, ok := v.(float64)
	if !ok || math.IsNaN(until) || math.IsInf(until, 0) || until < 0 {
		shown := "undefined"
		switch {
		case ok && math.IsNaN(until):
			shown = "NaN"
		case ok && math.IsInf(until, 1):
			shown = "Infinity"
		case ok && math.IsInf(until, -1):
			shown = "-Infinity"
		case ok:
			shown = jv.FormatNumber(until)
		case v != nil:
			shown = jv.Stringify(v)
		}
		return errorf("Task %d cannot sleep until %s: not a time in milliseconds", task, shown)
	}
	return nil
}
