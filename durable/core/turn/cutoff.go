// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// The restart limit of the built-in tasks (src/harness/live.ts cutOffTooOften, salvageable, cutOffCount, LIMITS_RESTARTS): a
// task cut off settings.restartLimit times in a row (the task record's `restarts`) ends itself visibly in its phase instead of
// running again; the scheduler faults a task of any other kind.

// LimitsRestarts reports whether the phases of tasks of kind end the task themselves at the restart limit.
func LimitsRestarts(kind string) bool {
	return kind == KindGeneration || kind == KindTool || kind == KindCompaction
}

// cutOffTooOften reports whether task was cut off settings.restartLimit times in a row, so its phase ends it instead of running.
// The settings are read only for a task that was cut off at all, so the normal path resolves nothing more.
func (r *run) cutOffTooOften(task *jv.Object) bool {
	restarts := num(task, "restarts")
	return restarts > 0 && restarts >= r.g.Host.Settings().RestartLimit
}

// salvageable reports whether a task at the limit may still do the cleanup it does when it ends, such as converting a partial:
// only at the limit. Past it, that cleanup may be what cut the task off, so the task ends without it.
func (r *run) salvageable(task *jv.Object) bool {
	return num(task, "restarts") <= r.g.Host.Settings().RestartLimit
}

// cutOffCount is how often a task was cut off, for a message: "once", or "3 times in a row".
func cutOffCount(task *jv.Object) string {
	restarts := int64(num(task, "restarts"))
	if restarts == 1 {
		return "once"
	}
	return itoa(restarts) + " times in a row"
}

// strIn is str of an object that may be absent.
func strIn(o *jv.Object, key string) string {
	if o == nil {
		return ""
	}
	return str(o, key)
}

// markEnded records, in the commit that ends the task for crashes (a terminal failure whose detail is restart_limit or
// interrupted), what reportEnded reports as task_interrupted: the reason, the message and the task's restart count
// (scheduler.ts commit).
func (r *run) markEnded(current *jv.Object, reason, message string) {
	r.endReason, r.endMessage = reason, message
	r.endRestarts, _ = current.Get("restarts")
}

// reportEnded is the continuation of the commit that ended the task: it reports a task that ended for crashes, then ends the phase.
func (r *run) reportEnded() {
	if r.endReason != "" {
		_ = r.rt.ReportInterrupted(errors.New(r.endMessage), r.endReason, r.endRestarts)
	}
	r.done(nil)
}
