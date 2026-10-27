// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// interruption is what a commit that ended a task for crashes reports (task_interrupted, types.ts ReportInfo): the task failed
// with an error whose detail.reason is restart_limit (it was cut off settings.restartLimit times in a row) or interrupted (a
// generation's run ended because the host kept stopping while the model answered), its message, and the task's restart count
// when it ended. The zero value is a commit that ended nothing for crashes.
type interruption struct {
	reason, message string
	restarts        float64
}

// interruptionOf is the interruption the commit of next, on the record current, makes (scheduler.ts runtime.commit, interruptionOf).
func interruptionOf(current, next *jv.Object) interruption {
	if status, _ := getStr(next, "status"); status != StatusTerminal {
		return interruption{}
	}
	outcome := getObj(next, "outcome")
	if outcome == nil {
		return interruption{}
	}
	if status, _ := getStr(outcome, "status"); status != "failed" {
		return interruption{}
	}
	failure, detail := getObj(outcome, "error"), (*jv.Object)(nil)
	if failure != nil {
		detail = getObj(failure, "detail")
	}
	if detail == nil {
		return interruption{}
	}
	reason, _ := getStr(detail, "reason")
	if reason != "restart_limit" && reason != "interrupted" {
		return interruption{}
	}
	message, _ := getStr(failure, "message")
	return interruption{reason: reason, message: message, restarts: restartsOf(current)}
}

// report reports that crashes ended this task. It runs after the commit that ended it, as the commit returns to the task, so the
// invocation may already be over.
func (r *Runtime) reportInterrupted(it interruption) {
	info := aboutInvocation("task_interrupted", r.inv)
	info.Set("reason", it.reason)
	info.Set("restarts", it.restarts)
	r.s.report(errors.New(it.message), info)
}

// ReportEnded reports what the host commit last finished with FinishHost ended the task for, if it ended it for crashes: the host's
// transaction returns to the task when the core answers its end.
func (r *Runtime) ReportEnded() {
	if it := r.hostEnded; it.reason != "" {
		r.hostEnded = interruption{}
		r.reportInterrupted(it)
	}
}
