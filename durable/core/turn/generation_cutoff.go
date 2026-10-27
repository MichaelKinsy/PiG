// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// interrupted handles a request or poll the host stopped during (the task record's `restarts`): a failed attempt under
// settings.retry (spec section 8.3). The committed partial becomes an aborted entry; the next attempt follows the backoff, a
// poll's by polling its deferred response again, or, with no retry left, the run ends interrupted (generation.ts interrupted).
func (r *run) interrupted(cp *jv.Object) {
	attempt := num(cp, "attempt")
	settings := r.g.Host.Settings()
	retry := settings.RetryEnabled && attempt <= settings.MaxRetries
	until := 0.0
	if retry {
		until = r.now() + RetryDelayMs(settings.BaseDelayMs, settings.MaxAgentDelayMs, attempt)
	}
	prefix := "The host stopped while the model was answering, on attempt "
	if str(cp, "phase") == phasePoll {
		prefix = "The host stopped while the deferred answer was fetched, on attempt "
	}
	r.ending = attemptEnding{cp: cp, text: prefix + itoa(int64(attempt)), reason: "interrupted", retry: retry, salvage: true, until: until}
	r.endAttempt()
}

// endCutOff ends a run whose generation was cut off settings.restartLimit times in a row (spec section 5.4). At the limit it
// still keeps a committed partial as an aborted entry and cancels a deferred response it polled; past the limit that cleanup may
// be what cut it off, so it ends without it (generation.ts endCutOff).
func (r *run) endCutOff(cp *jv.Object) {
	r.ending = attemptEnding{cp: cp, text: "The generation was interrupted " + cutOffCount(r.task) + " in its " + str(cp, "phase") + " phase", reason: "restart_limit", salvage: r.salvageable(r.task)}
	r.endAttempt()
}

// attemptEnding is how a generation attempt ends for crashes: with a retry after a backoff, or the end of the run.
type attemptEnding struct {
	cp     *jv.Object
	text   string
	reason string
	// retry: the next attempt follows after until; otherwise the run ends.
	retry bool
	// salvage: the committed partial becomes an aborted entry, and a deferred response is cancelled when the run ends.
	salvage bool
	until   float64
}

// endAttempt cancels the deferred response a run that ends no longer polls, then commits the ending.
func (r *run) endAttempt() {
	if model, handle := deferredOf(r.ending.cp); handle != nil && !r.ending.retry && r.ending.salvage {
		r.cancelDeferred(model, handle, r.commitEnding)
		return
	}
	r.commitEnding()
}

func (r *run) commitEnding() {
	r.commit(func(tx *session.Tx, current *jv.Object) (*jv.Object, error) {
		e := &r.ending
		// Read before the first table write; only a failure uses it.
		var failed failure
		if !e.retry {
			var err error
			if failed, err = r.prepareFailure(tx); err != nil {
				return nil, err
			}
		}
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		if e.salvage {
			if err := convertPartial(tx, live, r.conv); err != nil {
				return nil, err
			}
		}
		if e.retry {
			return r.sleepRetry(live)
		}
		r.markEnded(current, e.reason, e.text)
		if err := r.endFailedRun(tx, live, failed, UnansweredWith(e.reason, e.text)); err != nil {
			return nil, err
		}
		return failedTerminal(e.text, e.reason), nil
	}, r.reportEnded)
}

// sleepRetry records the failed attempt in pi.live and sleeps the backoff; the retry phase then prepares the next attempt, or
// polls the response of the poll the host died during again.
func (r *run) sleepRetry(live *delta.Obj) (*jv.Object, error) {
	e := &r.ending
	attempt := num(e.cp, "attempt")
	if err := live.Set("generation", jobj("attempt", attempt, "retry", jobj("at", e.until, "error", e.text))); err != nil {
		return nil, err
	}
	next := jobj("phase", phaseRetry, "attempt", attempt)
	withCompacted(next, idField(e.cp, "compacted"))
	next.Set("until", e.until)
	if str(e.cp, "phase") == phasePoll {
		poll := jv.NewObject()
		e.cp.Members(func(key string, value any) bool {
			switch key {
			case "model", "thinkingLevel", "cutoff", "handle":
				poll.Set(key, value)
			}
			return true
		})
		next.Set("poll", poll)
	}
	return jobj("status", "sleeping", "checkpoint", next, "until", e.until), nil
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
