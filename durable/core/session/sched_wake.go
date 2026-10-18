package session

import "github.com/MichaelKinsy/PiG/durable/core/jv"

// Wake is when the Session next needs a running process (scheduler.ts wake, Harness.wake): Active while some task can run
// now or is running, At for the earliest sleep not yet due.
type Wake struct {
	Active bool
	HasAt  bool
	At     float64
}

// notDue is a sleep whose until the clock has not reached; an abort mark ends a sleep (scheduler.ts #notDue).
func (s *Scheduler) notDue(rec *jv.Object) bool {
	if taskStatus(rec) != StatusSleeping || getBool(rec, "abortRequested") {
		return false
	}
	until, _ := getNum(getObj(rec, "state"), "until")
	return until > s.env.Now()
}

// wakeDue makes every sleeping task whose time has come a reservation candidate (scheduler.ts #wakeDue).
func (s *Scheduler) wakeDue() {
	s.sleeping.Range(func(id ID) bool {
		if rec, live := s.live.get(id); live && !s.notDue(rec) {
			s.runnable.Add(id)
		}
		return true
	})
}

// Wake is scheduler.ts wake: synchronous, from memory; a task whose chain is not loaded yet counts as it would alone.
//
// Wake runs after every commit, so its cost must not grow with the live tasks while one runs: the earliest sleep comes from
// the sleeping index, as wakeDue reads it, and the walk over the live tasks that looks for one that can run now (anyRunnable)
// is skipped when a running invocation or a reconcile pass with work already answers that. Walking every live task each
// time made ending a chain of 20,000 held tasks 20,000 walks of 20,000 records, each record a key search here, not the
// property read it is in scheduler.ts.
func (s *Scheduler) Wake() Wake {
	now := s.env.Now()
	// A reconcile pass with work to do is work too: it finalizes held outcomes and marks cascades, at open as recovery. A
	// process that dies before it lands must be woken again. A pass with nothing to do is not.
	active := len(s.invocations) > 0 || s.reconcilingWork > 0 || s.reconcileWork() || s.anyRunnable(now)
	var w Wake
	s.sleeping.Range(func(id ID) bool {
		rec, live := s.live.get(id)
		if !live || taskStatus(rec) != StatusSleeping || getBool(rec, "abortRequested") {
			return true
		}
		if until, _ := getNum(getObj(rec, "state"), "until"); until > now && (!w.HasAt || until < w.At) {
			w.HasAt, w.At = true, until
		}
		return true
	})
	w.Active = active
	return w
}

// anyRunnable is whether some live task can run now: not completing, not waiting on live work, not a sleep not yet due, not
// parked, and one a definition can take (or an abort-marked one, which is orphaned). It walks in insertion order and stops
// at the first, so a session with nothing to run pays one pass over its live tasks.
func (s *Scheduler) anyRunnable(now float64) bool {
	var snapshot Snapshot
	for _, rec := range s.live.values() {
		status := taskStatus(rec)
		if status == StatusCompleting {
			continue
		}
		aborted := getBool(rec, "abortRequested")
		if status == StatusWaiting && !aborted {
			live := false
			for _, id := range idsOf(getObj(rec, "state"), "on") {
				if _, ok := s.live.get(id); ok {
					live = true
					break
				}
			}
			if live {
				continue
			}
		}
		if status == StatusSleeping && !aborted {
			if until, _ := getNum(getObj(rec, "state"), "until"); until > now {
				continue
			}
		}
		if s.parked(rec) {
			continue
		}
		// An abort invocation waits for the live work below it, which counts itself.
		if aborted && len(s.waitingOn(rec, true)) > 0 {
			continue
		}
		// A task no definition can take waits for a registry change, except an abort-marked one, which is orphaned.
		if !aborted || rec.Has("abortReason") {
			if snapshot == nil {
				snapshot = s.env.Snapshot()
			}
			kind, _ := getStr(rec, "kind")
			if _, reason, _ := s.fit(rec, snapshot.Task(kind)); reason != "" {
				continue
			}
		}
		return true
	}
	return false
}

// scheduleWake recomputes the wake in a microtask, off the Session line's jobs: it arms the built-in timer, or tells OnWake
// of a change (scheduler.ts #scheduleWake).
func (s *Scheduler) scheduleWake() {
	if s.wakeScheduled || s.closing {
		return
	}
	s.wakeScheduled = true
	s.loop.Defer(func() {
		s.wakeScheduled = false
		if s.closing {
			return
		}
		w := s.Wake()
		if s.env.OnWake == nil {
			s.armWakeTimer(s.enabled && w.HasAt, w.At)
			return
		}
		// The host keeps what it delivered (HarnessOptions.onWake offers only changes, and again after a throw), so every
		// recomputation is offered to it.
		s.env.OnWake(w)
	})
}

// armWakeTimer keeps the timer for the earliest sleep. It rechecks the clock when it fires, so a wake before until only arms
// it again (scheduler.ts #armTimer).
func (s *Scheduler) armWakeTimer(armed bool, at float64) {
	if s.wakeTimer.set && armed && s.wakeTimer.at == at {
		return
	}
	s.cancelWakeTimer()
	if !armed || s.closing || s.env.WakeTimer == nil {
		return
	}
	s.wakeTimer.set, s.wakeTimer.at = true, at
	s.wakeTimer.cancel = s.env.WakeTimer(at, func() {
		s.wakeTimer.set, s.wakeTimer.cancel = false, nil
		s.wakeDue()
		s.kick()
	})
}

func (s *Scheduler) cancelWakeTimer() {
	if s.wakeTimer.set && s.wakeTimer.cancel != nil {
		s.wakeTimer.cancel()
	}
	s.wakeTimer.set, s.wakeTimer.cancel = false, nil
}

// SetOnWake makes the scheduler tell fn each change of Wake instead of arming a timer for sleeping tasks.
func (s *Scheduler) SetOnWake(fn func(w Wake)) { s.env.OnWake = fn }
