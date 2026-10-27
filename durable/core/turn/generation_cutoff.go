// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// interrupted handles a request or poll the host stopped during (the task record's `restarts`): a failed attempt under
// settings.retry (spec section 8.3). The committed partial becomes an aborted entry; the next attempt follows the backoff, a
// poll's by polling its deferred response again, or, with no retry left, the run ends interrupted (generation.ts interrupted).
func (r *run) interrupted(cp *jv.Object) {
	attempt := num(cp, "attempt")
	compacted := idField(cp, "compacted")
	settings := r.g.Host.Settings()
	retry := settings.RetryEnabled && attempt <= settings.MaxRetries
	until := 0.0
	if retry {
		until = r.now() + RetryDelayMs(settings.BaseDelayMs, settings.MaxAgentDelayMs, attempt)
	}
	polled := str(cp, "phase") == phasePoll
	text := "The host stopped while the model was answering, on attempt " + itoa(int64(attempt))
	if polled {
		text = "The host stopped while the deferred answer was fetched, on attempt " + itoa(int64(attempt))
	}
	commit := func() {
		r.commitReporting(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
			// Read before the first table write; only a failure uses it.
			var failed failure
			if !retry {
				var err error
				if failed, err = r.prepareFailure(tx); err != nil {
					return nil, err
				}
			}
			live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
			if err != nil {
				return nil, err
			}
			if err := convertPartial(tx, live, r.conv); err != nil {
				return nil, err
			}
			if retry {
				if err := live.Set("generation", jobj("attempt", attempt, "retry", jobj("at", until, "error", text))); err != nil {
					return nil, err
				}
				next := jobj("phase", phaseRetry, "attempt", attempt)
				withCompacted(next, compacted)
				next.Set("until", until)
				if polled {
					poll := jobj("model", objField(cp, "model"))
					if thinking := thinkingOf(cp); thinking != nil {
						poll.Set("thinkingLevel", thinking)
					}
					poll.Set("cutoff", float64(idField(cp, "cutoff")))
					poll.Set("handle", objField(cp, "handle"))
					next.Set("poll", poll)
				}
				return jobj("status", "sleeping", "checkpoint", next, "until", until), nil
			}
			if err := r.endFailedRun(tx, live, failed, UnansweredWith("interrupted", text)); err != nil {
				return nil, err
			}
			return failedTerminal(text, "interrupted"), nil
		})
	}
	// A run that ends does not leave the provider's job running.
	if !retry && polled {
		r.cancelDeferred(objField(cp, "model"), objField(cp, "handle"), commit)
		return
	}
	commit()
}

// endCutOff ends a run whose generation was cut off settings.restartLimit times in a row (spec section 5.4). At the limit it
// still keeps a committed partial as an aborted entry and cancels a deferred response it polled; past the limit that cleanup may
// be what cut it off, so it ends without it (generation.ts endCutOff).
func (r *run) endCutOff(cp *jv.Object) {
	text := "The generation was interrupted " + cutOffCount(r.task) + " in its " + str(cp, "phase") + " phase"
	salvage := r.salvageable(r.task)
	commit := func() {
		r.commitReporting(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
			failed, err := r.prepareFailure(tx)
			if err != nil {
				return nil, err
			}
			live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
			if err != nil {
				return nil, err
			}
			if salvage {
				if err := convertPartial(tx, live, r.conv); err != nil {
					return nil, err
				}
			}
			if err := r.endFailedRun(tx, live, failed, UnansweredWith("restart_limit", text)); err != nil {
				return nil, err
			}
			return failedTerminal(text, "restart_limit"), nil
		})
	}
	if model, handle := deferredOf(cp); salvage && handle != nil {
		r.cancelDeferred(model, handle, commit)
		return
	}
	commit()
}

// deferredOf is the deferred response a checkpoint polls, or will poll again after a retry's backoff: its model and handle.
func deferredOf(cp *jv.Object) (model, handle *jv.Object) {
	switch str(cp, "phase") {
	case phasePoll:
		return objField(cp, "model"), objField(cp, "handle")
	case phaseRetry:
		poll := objField(cp, "poll")
		return objField(poll, "model"), objField(poll, "handle")
	}
	return nil, nil
}

// cancelDeferred cancels a deferred response no attempt will poll again; a failure is reported, never thrown. then runs after.
func (r *run) cancelDeferred(model, handle *jv.Object, then func()) {
	ref := modelRefOf(model)
	if _, ok := r.g.Host.ContextWindow(ref); !ok {
		then()
		return
	}
	r.g.Host.CancelDeferred(ref, handle, r.rt.Signal(), func(err error) {
		if err != nil {
			_ = r.rt.Report(err, session.ReportFrom{Callback: "models"})
		}
		then()
	})
}
