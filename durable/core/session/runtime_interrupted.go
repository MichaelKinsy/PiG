// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

// ReportInterrupted reports that crashes ended this task (task_interrupted, types.ts ReportInfo): the task ended itself after
// being cut off settings.restartLimit times in a row (reason restart_limit), or a generation's run ended because the host kept
// stopping while the model answered (reason interrupted). restarts is the task's count when it ended. It runs in the commit's
// continuation, as scheduler.ts does after the commit that ended the task, so the invocation may already be over.
func (r *Runtime) ReportInterrupted(err error, reason, restarts any) error {
	info := aboutInvocation("task_interrupted", r.inv)
	info.Set("reason", reason)
	info.Set("restarts", restarts)
	r.s.report(err, info)
	return nil
}
