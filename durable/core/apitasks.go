// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// apiTasks serves the api ops of ABI section 8.1 that belong to the task scheduler: the memos of a running task, waits
// for tasks, outcomes and sleep. A request carrying `by` (the invoking task's ID) belongs to that invocation, so the
// scheduler rejects it once the invocation ends; a wait without `by` is the Harness's own. The answer is an api_result
// notice that may follow later steps. handled is false for an op that is not the scheduler's.
func (h *handler) apiTasks(id uint32, op string, req *jv.Object) (handled bool) {
	switch op {
	case "memo.get", "memo.put", "waitForTask", "waitForIdle", "outcomes", "ownedTasks", "sleepUntil", "wake", "retryTask":
	default:
		return false
	}
	reply := func(value any, err error) {
		if err != nil {
			h.apiError(id, err)
			return
		}
		h.apiOK(id, value)
	}
	if h.sched == nil {
		reply(nil, &Rejection{"Error", "the " + op + " request needs a Harness"})
		return true
	}
	if op == "wake" {
		reply(wakeJSON(h.sched.Wake()), nil)
		return true
	}
	if op == "retryTask" {
		// Harness.retryTask (scheduler.ts retry): clears a parked task's restart count and enables scheduling.
		h.sched.Retry(session.ID(numOf(req, "taskId")), nil, func(err error) { reply(nil, err) })
		return true
	}
	if op == "waitForIdle" {
		// Harness.waitForIdle / Conversation.waitForIdle (scheduler.ts waitForIdle): idle answers in the same microtask checkpoint,
		// closed rejects, otherwise the waiter settles with the scope's last live task.
		// The client methods (Harness.waitForIdle, Conversation.waitForIdle) are async functions that return the scheduler's promise (Pi 6c384aa3,
		// harness.ts). The handle an invocation gets is bound and returns it as it is.
		bound := req.Has("invocation")
		scope := session.ID(numOf(req, "conversationId"))
		settled := h.sched.IsIdle(scope)
		h.sched.WaitForIdle(scope, nil, func(err error) {
			if err != nil || bound || !h.holdFor(asyncReturnHops(settled), func() { reply(nil, nil) }) {
				reply(nil, err)
			}
		})
		return true
	}
	owner := session.ID(numOf(req, "by"))
	if owner == 0 && op != "waitForTask" {
		owner = session.ID(numOf(req, "taskId"))
	}
	rt := h.runtimes[owner]
	if rt == nil && (owner != 0 || op != "waitForTask") {
		reply(nil, &Rejection{"Error", "the " + op + " request names no running invocation"})
		return true
	}
	switch op {
	case "memo.get":
		value, found, err := rt.MemoGet(strOf(req, "name"))
		if !found {
			value = nil
		}
		reply(value, err)
	case "memo.put":
		candidate, _ := req.Get("candidate")
		rt.Memo(strOf(req, "name"), candidate, func(winner any, err error) { reply(winner, err) })
	case "waitForTask":
		// The caller's `await readOnLine(...)` resumes at the job's run, then `return found.promise` of an async function adopts it:
		// an already settled promise answers three hops later, a waiter's promise one hop after it resolves (scheduler.ts :306-316).
		then := func(record *jv.Object, err error, waited bool) {
			hops := 3
			if waited {
				hops = 1
			}
			// Harness.waitForTask is an async function that returns the scheduler's promise (harness.ts, Pi 6c384aa3); a wait made by the
			// runtime of a task returns it as it is.
			if rt == nil {
				hops += asyncReturnHops(false)
			}
			if err != nil || !h.holdFor(hops, func() { reply(record, err) }) {
				reply(record, err)
			}
		}
		taskID := session.ID(numOf(req, "taskId"))
		if rt != nil {
			rt.WaitForTask(taskID, then)
		} else {
			h.sched.WaitForTaskFound(taskID, nil, then)
		}
	case "outcomes":
		ids, _ := req.Get("taskIds")
		list, _ := ids.([]any)
		tasks := make([]session.ID, len(list))
		for i, v := range list {
			n, _ := v.(float64)
			tasks[i] = session.ID(n)
		}
		rt.Outcomes(tasks, func(outcomes []*jv.Object, err error) {
			out := make([]any, len(outcomes))
			for i, o := range outcomes {
				out[i] = o
			}
			reply(out, err)
		})
	case "ownedTasks":
		rt.OwnedTasks(func(records []*jv.Object, err error) {
			out := make([]any, len(records))
			for i, r := range records {
				out[i] = r
			}
			reply(out, err)
		})
	case "sleepUntil":
		checkpoint, _ := req.Get("checkpoint")
		rt.SleepUntil(numOf(req, "until"), checkpoint, func(err error) { reply(nil, err) })
	}
	return true
}

// asyncReturnHops is the ticks an async function adds when it returns a promise another function made, to the caller that awaits it: the thenable job
// registers the function's promise on the inner one a tick after the return, and the reaction that settles it runs a tick after the inner promise
// settled. A promise that has already settled at the return costs two; a pending one costs one.
func asyncReturnHops(settled bool) int {
	if settled {
		return 2
	}
	return 1
}

// wakeJSON is a Wake on the wire: {active, at?}.
func wakeJSON(w session.Wake) *jv.Object {
	o := jv.NewObject()
	o.Set("active", w.Active)
	if w.HasAt {
		o.Set("at", w.At)
	}
	return o
}
