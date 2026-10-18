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
}

func (s *Scheduler) run(r *reservation, finish func()) {
	inv := r.inv
	st := &runState{task: r.task, snapshot: r.snapshot}
	rt := s.newRuntime(inv, func() *TaskImpl { return st.task }, func() Snapshot { return st.snapshot })
	var previous *phaseResult
	var next func()
	next = func() {
		prev := previous
		s.step(inv, func(tx *Tx, current *jv.Object) (decision, error) {
			return s.decide(tx, current, prev, st, inv.extension)
		}, func(current *jv.Object) {
			if current == nil || s.closing {
				finish()
				return
			}
			// #run awaits #step, an async function that awaited the step's commit: the phase starts a tick after the job resolved
			// (scheduler.ts :919-926, :1000-1002).
			s.loop.NextTick(func() {
				cp, _ := getObj(current, "state").Get("checkpoint")
				rt.agentReset()
				st.task.Phase(rt, current, func(err error) {
					previous = &phaseResult{checkpoint: cp, failure: err}
					s.loop.Defer(next)
				})
			})
		})
	}
	next()
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
			s.report(errorf("Task %d keeps running under its old %s definition", mustInt(current, "id"), kind), info)
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
	rt := s.newRuntime(inv, func() *TaskImpl { return r.task }, func() Snapshot { return r.snapshot })
	after := func(failure error) {
		s.step(inv, func(*Tx, *jv.Object) (decision, error) {
			if failure != nil {
				return decision{kind: decideFault, fault: failure}, nil
			}
			return decision{kind: decideFault, fault: errorf("Abort handler of task %d returned without a terminal outcome", inv.taskID)}, nil
		}, func(*jv.Object) { finish() })
	}
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
func (s *Scheduler) step(inv *invocation, decide func(tx *Tx, current *jv.Object) (decision, error), then func(current *jv.Object)) {
	var result *jv.Object
	var fault error
	// The fault is reported as the step resumes from its commit, after its publication and before the line's next job.
	reportFault := func() {
		if fault != nil {
			info := aboutInvocation("task_faulted", inv)
			info.Set("reason", "threw")
			s.report(fault, info)
		}
	}
	s.commitJobAfter(s.loop.LineAfterTail, nil, func(tx *Tx) error {
		result = nil
		fault = nil
		found, ok := s.live.get(inv.taskID)
		var current *jv.Object
		if ok && taskStatus(found) == StatusRunning {
			current = found
		}
		d := decision{kind: decideStop}
		if current != nil && !s.closing {
			var err error
			if d, err = decide(tx, current); err != nil {
				return err
			}
		}
		if d.kind == decideContinue {
			result = current
			return nil
		}
		s.endInvocation(inv)
		if d.kind == decideFault {
			fault = d.fault
			msg := faultText(d.fault)
			return s.terminate(tx, current, obj("status", "faulted", "error", obj("message", msg)))
		}
		return nil
	}, TxScope{}, reportFault, func(err error) {
		if err != nil {
			s.endInvocation(inv)
			s.failSession(err)
			then(nil)
			return
		}
		then(result)
	})
}

// terminate settles a task with a scheduler-written outcome: completing while
// owned work is live, else terminal with the harness settlement.
func (s *Scheduler) terminate(tx *Tx, rec *jv.Object, outcome *jv.Object) error {
	if _, err := s.loadScopes(false); err != nil {
		return err
	}
	if s.hasOwnedLive(mustInt(rec, "id"), overlayOf(tx), nil) {
		return tx.stageTask(withState(rec, obj("status", StatusCompleting, "outcome", outcome)))
	}
	if err := tx.stageTask(withState(rec, obj("status", StatusTerminal, "outcome", outcome))); err != nil {
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
			return tx.stageTask(withState(progressed(current), obj("status", StatusCompleting, "outcome", outcome)))
		}
	}
	// A state the task committed that differs from its current one is progress: its restart count starts over. The same
	// checkpoint committed again, for example as intent before an effect, is not.
	if jv.Equal(getObj(current, "state"), next) {
		return tx.stageTask(withState(current, next))
	}
	return tx.stageTask(withState(progressed(current), next))
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
