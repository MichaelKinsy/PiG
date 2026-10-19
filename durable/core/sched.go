// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"math"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// registrySnapshot is the task definitions of the registry (session.Snapshot): the core's built-in tasks and the host's
// custom ones. A snapshot returns the same *TaskImpl for a name on every call, because the scheduler detects a reload by
// the identity of the definition between phases.
type registrySnapshot struct{ tasks map[string]*session.TaskImpl }

func (r *registrySnapshot) Task(name string) *session.TaskImpl { return r.tasks[name] }

// tracked wraps a task definition so that each phase and abort handler keeps its invocation's Runtime in
// handler.runtimes while it runs: api requests and transactions that name the invocation find it there.
func (h *handler) tracked(impl *session.TaskImpl) *session.TaskImpl {
	track := func(run func(*session.Runtime, *jv.Object, func(error))) func(*session.Runtime, *jv.Object, func(error)) {
		if run == nil {
			return nil
		}
		return func(rt *session.Runtime, task *jv.Object, done func(error)) {
			id := rt.TaskID()
			// The runtime stays findable until its invocation ends, not only while a phase runs: a commit the handler kept the
			// runtime for may queue behind the commit that ends the invocation, and is then rejected by the line job, as
			// scheduler.ts #gated rejects it.
			if h.runtimes[id] != rt {
				h.runtimes[id] = rt
				rt.OnEnded(func() {
					if h.runtimes[id] == rt {
						delete(h.runtimes, id)
					}
				})
			}
			run(rt, task, done)
		}
	}
	wrapped := *impl
	wrapped.Phase, wrapped.Abort = track(impl.Phase), track(impl.Abort)
	return &wrapped
}

// startScheduler builds the Harness mode machinery on the Session: the Loop that serializes line jobs behind host
// transactions, and the task scheduler of harness/scheduler.ts, opened over the live tasks.
func (h *handler) startScheduler() error {
	h.loop = &session.Loop{
		Blocked: func() bool { return h.holder != 0 },
		Admit:   h.sess.Admit,
		Session: h.sess,
		Started: func() { h.job = jobStats{mintStart: h.sess.NextID()}; h.sess.TakeTicks() },
		Hold:    h.holdJob,
		Tail: func(release func()) bool {
			if h.loop.Answered {
				return h.holdFor(2, release)
			}
			return h.holdFor(tailHops, release)
		},
		Begin:   func(start func()) bool { return h.holdFor(1, start) },
		Retain:  h.holdOrigin,
		Release: h.dropOrigin,
	}
	h.runtimes = map[session.ID]*session.Runtime{}
	h.loadRegistry(objOf(h.open, "registry"))
	h.sched = session.NewScheduler(h.sess, h.loop, session.Env{
		Snapshot:           func() session.Snapshot { return h.snap },
		FreshSnapshot:      h.freshSnapshot,
		Report:             h.report,
		RestartLimit:       h.restartLimit,
		BlockedChanged:     h.blockedChanged,
		Now:                func() float64 { return h.now },
		Timer:              h.armBackgroundTimer,
		WakeTimer:          h.armWakeTimer,
		ContextRetentionMs: h.contextRetentionMs,
		ReleaseContext:     h.releaseContext,
		SettleOutcome:      turn.SettleSchedulerOutcome,
		WithdrawInputs:     inbox.WithdrawQueued,
		ReadTicks:          func() int { return h.job.change },
		GateRead:           func(method string, id session.ID) { _ = h.gateRead(method, float64(id)) },
		After:              h.hopsThen,
	})
	if onWake, _ := boolOf(h.open, "onWake"); onWake {
		// The host wakes its process itself: the core arms no timer for sleeping tasks and publishes each change of the wake.
		h.sched.SetOnWake(h.publishWake)
	}
	finished := false
	var openErr error
	// Harness.open resolves when the scheduler's open job has: its statements (recovery of interrupted tasks) are in this step, not on
	// the COMMIT hop of a job whose completion nobody waits for (hops.go holdJob).
	h.opening = true
	h.sched.Open(func(err error) {
		finished, openErr = true, err
		h.openFinished(err)
	})
	err := h.pump()
	h.opening = false
	if err != nil {
		return err
	}
	if !finished {
		// The host's Storage holds the reconciliation commit (gate.go): the open's outcome follows as the answer of the
		// api request "opened".
		return nil
	}
	return openErr
}

// openFinished records how a scheduler open that waited for the host's Storage ended and answers the "opened" requests.
func (h *handler) openFinished(err error) {
	h.openDone, h.openErr = true, err
	for _, id := range h.openWaiters {
		h.answerOpened(id)
	}
	h.openWaiters = nil
}

func (h *handler) answerOpened(id uint32) {
	if h.openErr != nil {
		h.apiError(id, h.openErr)
		return
	}
	h.apiOK(id, nil)
}

// freshSnapshot is registry.snapshot() inside a step job (scheduler.ts:955 #decide): the host calls the user's reader
// (hook registrySnapshot), which may run any user code, close() included. The step job parks until the answer, then
// reruns and takes the answered snapshot.
func (h *handler) freshSnapshot() (session.Snapshot, error) {
	switch h.snapAsk {
	case snapAnswered:
		h.snapAsk = snapIdle
		if err := h.snapErr; err != nil {
			h.snapErr = nil
			return nil, err
		}
		return h.snap, nil
	case snapAsking:
		return nil, &session.NeedRead{}
	}
	h.snapAsk = snapAsking
	body := jv.NewObject()
	body.Set("name", "registrySnapshot")
	body.Set("handler", float64(0))
	body.Set("conversationId", float64(0))
	body.Set("taskId", float64(0))
	body.Set("payload", nil)
	// The answer applies at once rather than as a Loop continuation: the Loop is parked on this job, as nothing else
	// runs while Pi's synchronous snapshot() call does.
	h.startEffect(abi.EffectHook, jv.Append(nil, body), &pending{done: func(outcome uint8, payload []byte) {
		value, err := jv.Parse(payload)
		if err == nil && outcome != abi.OutcomeResult {
			err = thrown(value)
		}
		if err != nil {
			h.snapErr = err // the reader threw inside the step job: the step's commit rejects with it
		} else if registry := asObject(value); registry != nil {
			h.loadRegistry(registry)
		}
		h.snapAsk = snapAnswered
		h.resumeWait = true
	}})
	return nil, &session.NeedRead{}
}

// Registry snapshot request states of freshSnapshot.
const (
	snapIdle uint8 = iota
	snapAsking
	snapAnswered
)

// pump runs the Loop until it has nothing to do: after every event, and after the line is released. A job parked on a
// host answer (a *NeedRead with no reads) waits for it; the answer's continuation resumes the job.
func (h *handler) pump() error {
	defer h.flushHeld() // statements of a job that no hold parked (a bare job, a refused hold) leave with the step
	if h.loop == nil {
		return nil
	}
	h.unpin()
	h.loop.Kick()
	err := h.loop.Run()
	for err != nil {
		need := h.loop.Parked()
		if need == nil || len(need.Reads) != 0 {
			return &Rejection{"Error", "a scheduler job needs a host read: " + err.Error()}
		}
		if !h.resumeWait {
			// The microtasks queued behind the parked job wait for the host's answer across steps.
			h.pin()
			return nil
		}
		h.resumeWait = false
		err = h.loop.Resume()
	}
	return nil
}

// onTimer fires the timer the core armed: a timer effect's event (ABI section 5, kind 12).
//
//go:noinline
func (h *handler) onTimer(ev *abi.Event) error {
	if fire := h.takeTimer(ev.ID); fire != nil {
		fire()
	}
	return nil
}

// report forwards a non-fatal failure to HarnessOptions.onReport as a report notice: {name, message, info}, info being the
// ReportInfo of types.ts (type, and task, extension, reason, callback or conversationId where they apply).
func (h *handler) report(err error, info *jv.Object) {
	body := errorJSON(err)
	body = append(body[:len(body)-1], `,"info":`...)
	body = jv.Append(body, info)
	body = append(body, '}')
	h.b.Notice(abi.NoticeReport, body)
}

// reportFrom reports a failure of code a task's phase called: through the task's runtime, which names the task, or
// without a task when the invocation is not known (generation.ts runtime.report).
func (h *handler) reportFrom(task session.ID, err error, from session.ReportFrom) {
	if rt := h.runtimes[task]; rt != nil {
		_ = rt.Report(err, from)
		return
	}
	if from.Callback != "" {
		h.report(err, jobj("type", "host_error", "callback", from.Callback))
		return
	}
	info := jobj("type", "extension_error")
	if from.Extension != "" {
		info.Set("extension", from.Extension)
	}
	h.report(err, info)
}

// onRegistry takes the host's new registry snapshot (ABI section 5, kind 4): it may make blocked tasks runnable.
//
//go:noinline
func (h *handler) onRegistry(ev *abi.Event) error {
	v, err := jv.Parse(ev.Payload)
	if err != nil {
		return err
	}
	req, _ := v.(*jv.Object)
	// The host's Storage methods that override Pi's changed (a method assigned after open): the gate list is sent again.
	if list, ok := req.Get("storageGate"); ok && h.open != nil {
		h.open.Set("storageGate", list)
		h.gate = nil
		return nil
	}
	// Harness settings are live in Pi (resolveSettings(options.settings) at each read): the host sends them again when
	// they change.
	if settings := objOf(req, "settings"); settings != nil && h.open != nil {
		h.open.Set("settings", settings)
		return nil
	}
	if h.sched == nil {
		return nil
	}
	h.loadRegistry(objOf(req, "registry"))
	h.sched.RegistryChanged()
	return nil
}

// releaseContext frees the transcript the core derived a conversation's context from once Pi's scheduler drops its kept
// range for idleness (history.Store.Release).
func (h *handler) releaseContext(conv session.ID) {
	if h.hist != nil {
		h.hist.Release(int64(conv))
	}
	h.releaseHostContext(conv)
}

// restartLimit is settings.restartLimit, five unless the host set it (harness/agent.ts restartLimit): a limit that is not a
// number of restarts, 1 or more, means the default, and Infinity never parks (the host sends it as the largest finite number).
func (h *handler) restartLimit() float64 {
	if v, ok := valueOf(objOf(h.open, "settings"), "restartLimit").(float64); ok && v >= 1 {
		return math.Floor(v)
	}
	return 5
}

// afterFailedRun is settings.afterFailedRun, keep unless the host set it.
func (h *handler) afterFailedRun() string {
	if v := strOf(objOf(h.open, "settings"), "afterFailedRun"); v != "" {
		return v
	}
	return "keep"
}

// contextRetentionMs is settings.contextRetentionMs, ten minutes unless the host set it (harness/agent.ts).
func (h *handler) contextRetentionMs() float64 {
	if v, ok := valueOf(objOf(h.open, "settings"), "contextRetentionMs").(float64); ok {
		return v
	}
	return 600000
}

// documentRetentionMs is settings.documentRetentionMs; nil when the host set none, which means the default (harness/agent.ts).
func (h *handler) documentRetentionMs() *float64 {
	if v, ok := valueOf(objOf(h.open, "settings"), "documentRetentionMs").(float64); ok {
		return &v
	}
	return nil
}

// publishWake offers the host the Harness's wake (HarnessOptions.onWake) in a wake notice after each recomputation: the host
// delivers only a changed value, and delivers it again after a throw, so it keeps the last value it delivered.
func (h *handler) publishWake(w session.Wake) {
	h.b.Notice(abi.NoticeWake, jv.Append(nil, wakeJSON(w)))
}
