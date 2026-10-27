// Ports packages/durable/src/harness/scheduler.ts (the telemetry of #phase, #countActive and #resumed, Pi 2ec55d80 "Durable and
// pi-ai telemetry").

package session

import (
	"slices"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// PhaseOutcome is the `outcome` attribute of a phase span: `next=<phase>`, waiting, sleeping, done, faulted or aborted.
type PhaseOutcome uint8

// Phase outcomes.
const (
	PhaseNext PhaseOutcome = iota + 1
	PhaseWaiting
	PhaseSleeping
	PhaseDone
	PhaseFaulted
	PhaseAborted
)

// PhaseInfo is what the host needs to start the `pi.durable.phase` span of one phase invocation (scheduler.ts #phase). The host
// answers with Started, and the invocation runs the phase once it has.
type PhaseInfo struct {
	Task    ID
	Kind    string
	Phase   string
	Abort   bool
	Resumed bool
	// Attempt is the checkpoint's attempt, when HasAttempt.
	Attempt    float64
	HasAttempt bool
	// Trace is the record's stored carrier, the span's parent; nil starts at the Harness's root.
	Trace any
	// Links are the record's creation-time carriers, given to the first invocation only (invocation.links).
	Links any

	inv *invocation
}

// Started is the host's answer to StartPhase: its handle of the span, and the span's carrier (propagation.inject(span)), which
// the tasks the phase creates inherit as their trace. The invocation runs its phase as the call returns.
func (p *PhaseInfo) Started(span uint32, carrier any) {
	inv := p.inv
	inv.span, inv.carrier = span, carrier
	inv.signal.Span = span
	p.inv = nil
	start := inv.start
	inv.start = nil
	if start == nil {
		// An abort handler: the span was asked for before it ran.
		a := inv.abort
		inv.abort = abortRun{}
		inv.s.abortHandler(a.r, a.rt, a.current, a.after)
		return
	}
	start()
}

// Answered is Started for the host's answer to the span hook, `{span, carrier?}`: a failed hook starts the phase without a span.
func (p *PhaseInfo) Answered(v any, err error) {
	var span uint32
	var carrier any
	if o, ok := v.(*jv.Object); ok && err == nil {
		n, _ := getNum(o, "span")
		span = uint32(n)
		carrier, _ = o.Get("carrier")
	}
	p.Started(span, carrier)
}

// Telemetry is the host's side of Pi's telemetry: the spans and metrics of `DURABLE_TELEMETRY_SCHEMA`. A scheduler without one
// records nothing (HarnessOptions.telemetry omitted). The core never holds a span: it names them by the handle the host answered
// StartPhase with, or was given by the call whose Context carries it (TxScope.Span).
type Telemetry interface {
	// StartPhase starts the invocation's span and calls p.Started with it.
	StartPhase(p *PhaseInfo)
	// EndPhase ends the phase span with its outcome. next is the checkpoint's phase of PhaseNext (`next=<phase>`); a
	// PhaseFaulted outcome names how the task ended (faulted, failed or orphaned: the status task_<ended>) and carries the
	// diagnostic message in full.
	EndPhase(span uint32, outcome PhaseOutcome, next, ended, message string)
	// Checkpoint is the `pi.durable.checkpoint` event of a commit that moved the running checkpoint to phase.
	Checkpoint(span uint32, phase string)
	// ToolCall sets the called tool's name and call ID on the span (tool.ts readCall).
	ToolCall(span uint32, name, callID string)
	// Replay is the `pi.durable.replay` event of a nested tool call answered from its stored result.
	Replay(span uint32, name string)
	// Active counts a task of kind in or out of `pi.durable.tasks.active`.
	Active(kind string, delta int)
}

// startPhase starts the span of the phase the invocation is about to run and then runs start (scheduler.ts #phase: the handler
// runs inside the span's callback). Without a Telemetry the phase starts at once.
func (s *Scheduler) startPhase(inv *invocation) {
	task := inv.record
	cp := getObj(getObj(task, "state"), "checkpoint")
	info := &inv.info
	*info = PhaseInfo{Task: inv.taskID, Kind: inv.kind, Abort: inv.abortMode, Resumed: s.wasResumed(inv.taskID), inv: inv}
	info.Phase, _ = getStr(cp, "phase")
	if v, ok := cp.Get("attempt"); ok {
		info.Attempt, info.HasAttempt = v.(float64)
	}
	info.Trace, _ = task.Get("trace")
	info.Links = inv.links
	s.env.Telemetry.StartPhase(info)
}

// wasResumed takes a task off the tasks the Harness found live at open: the span of its first invocation is `resumed`
// (scheduler.ts #resumed.delete).
func (s *Scheduler) wasResumed(id ID) bool {
	at := slices.Index(s.resumed, id)
	if at < 0 {
		return false
	}
	s.resumed = slices.Delete(s.resumed, at, at+1)
	return true
}

// endPhase ends the span of the phase the invocation ran, after its decision step (scheduler.ts #phase, its `finally`).
func (s *Scheduler) endPhase(inv *invocation) {
	span := inv.span
	inv.span, inv.carrier = 0, nil
	inv.signal.Span = 0
	if span == 0 || s.env.Telemetry == nil {
		return
	}
	// The terminal outcome of the task, when it has one: the scheduler's own fault, else what the task committed.
	terminal := inv.fault
	state := getObj(inv.record, "state")
	st, _ := getStr(state, "status")
	if terminal == nil && (st == StatusTerminal || st == StatusCompleting) {
		terminal = getObj(state, "outcome")
	}
	var outcome PhaseOutcome
	var ended string
	switch {
	case terminal != nil:
		ended, _ = getStr(terminal, "status")
		switch ended {
		case "completed":
			outcome = PhaseDone
		case "aborted":
			outcome = PhaseAborted
		default:
			outcome = PhaseFaulted
		}
	case st == StatusWaiting:
		outcome = PhaseWaiting
	case st == StatusSleeping:
		outcome = PhaseSleeping
	case inv.ended || s.closing:
		outcome = PhaseAborted
	default:
		outcome = PhaseNext
	}
	message := ""
	if outcome == PhaseFaulted {
		if ended == "orphaned" {
			message, _ = getStr(terminal, "reason")
		} else if e := getObj(terminal, "error"); e != nil {
			message, _ = getStr(e, "message")
		}
	}
	next := ""
	if outcome == PhaseNext {
		next, _ = getStr(getObj(state, "checkpoint"), "phase")
	}
	s.env.Telemetry.EndPhase(span, outcome, next, ended, message)
}

// countActive counts a task in or out of `pi.durable.tasks.active` (scheduler.ts #countActive).
func (s *Scheduler) countActive(rec *jv.Object, delta int) {
	if s.env.Telemetry == nil {
		return
	}
	kind, _ := getStr(rec, "kind")
	s.env.Telemetry.Active(kind, delta)
}

// trackActive is the telemetry of #track: a task that ends is counted out and forgotten as resumed, and one seen for the first
// time is counted in (a Harness that is closing counts nothing: its seal counted every live task out).
func (s *Scheduler) trackActive(id ID, prev, rec *jv.Object) {
	if taskStatus(rec) == StatusTerminal {
		if prev != nil && !s.closing {
			s.countActive(rec, -1)
		}
		s.wasResumed(id)
		return
	}
	if prev == nil && !s.closing {
		// A job restarted from scratch (Open's callback after the host's rows) tracks the same records again.
		if _, known := s.live.get(id); !known {
			s.countActive(rec, 1)
		}
	}
}

// sealActive counts every live task out at the seal (scheduler.ts #seal).
func (s *Scheduler) sealActive() {
	if s.env.Telemetry == nil {
		return
	}
	for _, rec := range s.live.values() {
		s.countActive(rec, -1)
	}
	s.resumed = nil
}

// addResumed notes a task found live at open (scheduler.ts #resumed.add); a restarted open job notes it again.
func (s *Scheduler) addResumed(id ID) {
	if s.env.Telemetry != nil && !slices.Contains(s.resumed, id) {
		s.resumed = append(s.resumed, id)
	}
}

// reportAt is report with the span of the Context the failure is reported under (scheduler.ts #report's context).
func (s *Scheduler) reportAt(err error, info *jv.Object, span uint32) {
	s.reportSpan = span
	s.report(err, info)
	s.reportSpan = 0
}

// movedCheckpoint is the phase a running state moves the task's checkpoint to, empty when the state is not a running one or
// keeps the phase (scheduler.ts runtime.commit).
func movedCheckpoint(current, next *jv.Object) string {
	if status, _ := getStr(next, "status"); status != StatusRunning {
		return ""
	}
	to, _ := getStr(getObj(next, "checkpoint"), "phase")
	from, _ := getStr(getObj(getObj(current, "state"), "checkpoint"), "phase")
	if to == from {
		return ""
	}
	return to
}

// ToolCall sets the called tool's name and call ID on the phase's span (tool.ts readCall).
func (r *Runtime) ToolCall(name, callID string) {
	if t := r.s.env.Telemetry; t != nil && r.inv.span != 0 {
		t.ToolCall(r.inv.span, name, callID)
	}
}

// Replay records that a nested tool call was answered from its stored result (tool.ts: the `pi.durable.replay` event).
func (r *Runtime) Replay(name string) {
	if t := r.s.env.Telemetry; t != nil && r.inv.span != 0 {
		t.Replay(r.inv.span, name)
	}
}

// SpanOf is the handle of the span of the phase task's invocation is running, 0 when it is in none.
func (s *Scheduler) SpanOf(id ID) uint32 {
	if inv := s.invocations[id]; inv != nil {
		return inv.span
	}
	return 0
}

// Span is the handle of the span of the phase the runtime's invocation is running, 0 when it is in none.
func (r *Runtime) Span() uint32 { return r.inv.span }

// checkpointMoved records `pi.durable.checkpoint` for a commit that moved the running task's checkpoint to another phase
// (scheduler.ts runtime.commit): the invocation's own commits are the only ones that change a running checkpoint's phase.
func (s *Scheduler) checkpointMoved(inv *invocation, prev, rec *jv.Object) {
	if s.env.Telemetry == nil || prev == nil || inv.span == 0 {
		return
	}
	if phase := movedCheckpoint(prev, getObj(rec, "state")); phase != "" {
		s.env.Telemetry.Checkpoint(inv.span, phase)
	}
}
