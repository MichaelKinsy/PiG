// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// The built-in pi.compaction task (src/harness/compaction.ts, spec section 8.7): select an old prefix of the model context,
// summarize it, and place a summary entry whose head is the first kept entry. A compaction the generation owns blocks it
// and appends directly; a conversation-owned one places its summary through a write submission. The pure parts (range
// selection, prompts, decisions, drafts, checkpoints) are package history's.

// Compaction phases.
const (
	phaseSelect    = "select"
	phaseSummarize = "summarize"
)

// CompactionTask implements the pi.compaction task over a Host.
type CompactionTask struct{ Host *Host }

// Impl returns the executable definition registered in the registry.
func (g *CompactionTask) Impl() *session.TaskImpl {
	return &session.TaskImpl{Def: *CompactionDef, Phase: g.phase, Abort: g.abort, AbortReturnHops: abortReturnHops}
}

type compactionRun struct {
	*run
	task *jv.Object
}

func (g *CompactionTask) newRun(rt *session.Runtime, task *jv.Object, done func(error)) *compactionRun {
	gen := &Generation{Host: g.Host}
	r := gen.newRun(rt, done)
	r.kind = KindCompaction
	return &compactionRun{run: r, task: task}
}

func (g *CompactionTask) phase(rt *session.Runtime, task *jv.Object, done func(error)) {
	r := g.newRun(rt, task, done)
	cp := checkpointOf(task)
	switch str(cp, "phase") {
	case phaseSelect:
		r.selectPhase()
	case phaseSummarize:
		r.summarize(cp)
	case phaseRetry:
		r.retry(cp)
	default:
		done(errorString("Compaction phase " + str(cp, "phase") + " is unknown"))
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func (r *compactionRun) input() *jv.Object { return objField(r.task, "input") }

// removeStatus removes this task's status from pi.live in the commit.
func (r *compactionRun) removeStatus(tx *session.Tx) (*deltaObj, error) {
	live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
	if err != nil {
		return nil, err
	}
	return live, RemoveCompactionStatus(live, r.rt.TaskID())
}

// complete removes the status and completes without a summary.
func (r *compactionRun) complete() {
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		if _, err := r.removeStatus(tx); err != nil {
			return nil, err
		}
		return jobj("status", "terminal", "outcome", jobj("status", "completed", "result", jv.NewObject())), nil
	})
}

func (r *compactionRun) failNoModel(ref *ModelRef) {
	message := "No model is configured"
	if ref != nil {
		message = "Model " + ref.Provider + "/" + ref.ModelID + " is not available"
	}
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		if err := FailCompactionStatus(live, r.rt.TaskID(), message); err != nil {
			return nil, err
		}
		return jobj("status", "terminal", "outcome", jobj("status", "failed", "error", jobj("message", message, "detail", jobj("reason", "no_model")))), nil
	})
}

func (r *compactionRun) selectPhase() {
	h := r.g.Host
	r.agent(func(agent *Agent) {
		settings := h.Settings()
		known := false
		if agent.Model != nil {
			_, known = h.ContextWindow(*agent.Model)
		}
		r.context(0, func(view *history.View) {
			policy := settings.Compaction
			if agent.Model != nil {
				policy = settings.CompactionFor(*agent.Model)
			}
			params := &history.SelectParams{ModelPresent: agent.Model != nil, ModelKnown: known, Policy: policy}
			if agent.Model != nil {
				params.ModelProvider, params.ModelID = []byte(agent.Model.Provider), []byte(agent.Model.ModelID)
				params.Model = []byte(jv.Stringify(agent.Model.object()))
				params.ThinkingLevel = []byte(jv.Stringify(agent.ThinkingLevel))
				params.StreamOptions = []byte(jv.Stringify(streamOrEmpty(settings.Stream)))
				params.ModelMaxTokens = h.MaxTokens(*agent.Model)
			}
			plan := history.PlanSelect(view, params)
			switch plan.Outcome {
			case history.SelectFailNoModel:
				r.failNoModel(agent.Model)
			case history.SelectNothing:
				r.complete()
			default:
				r.beforeCompact(view, plan)
			}
		})
	})
}

func (r *compactionRun) beforeCompact(view *history.View, plan history.SelectPlan) {
	input := r.input()
	instructions, hasInstructions := stringField(input, "instructions")
	var decision *jv.Object
	r.eachHook("beforeCompact", func() *jv.Object {
		raw := history.AppendBeforeCompactPayload(nil, view, plan.Cut, str(input, "reason"), plan.FirstKept, []byte(instructions), hasInstructions)
		return jobj("compaction", parseJSONObject(raw))
	}, func(result any) bool {
		if o, ok := result.(*jv.Object); ok {
			decision = o
			return true
		}
		return false
	}, func(err error) {
		if err != nil {
			r.fail(err)
			return
		}
		if decision != nil {
			if d, ok := decision.Get("decline"); ok && d != nil {
				r.complete()
				return
			}
			if _, ok := decision.Get("summary"); ok {
				r.place(plan.FirstKept, str(decision, "summary"))
				return
			}
		}
		raw, err := history.AppendCheckpoint(nil, &plan.Checkpoint)
		if err != nil {
			r.fail(err)
			return
		}
		next := parseJSONObject(raw)
		r.commitDone(func(*session.Tx, *jv.Object) (*jv.Object, error) {
			return jobj("status", "running", "checkpoint", next), nil
		})
	})
}

// parseJSONObject parses JSON the history package produced; it is valid by construction.
func parseJSONObject(raw []byte) *jv.Object {
	v, err := jv.Parse(raw)
	if err != nil {
		return jv.NewObject()
	}
	o, _ := v.(*jv.Object)
	return o
}

func (r *compactionRun) summarize(cp *jv.Object) {
	h := r.g.Host
	ref := modelRefOf(objField(cp, "model"))
	attempt := num(cp, "attempt")
	if _, ok := h.ContextWindow(ref); !ok {
		r.failNoModel(&ref)
		return
	}
	// The context at tail is immutable, so this is the range select chose.
	r.context(idField(cp, "tail"), func(view *history.View) {
		cut := view.CutOf(int64(idField(cp, "firstKept")))
		now := r.now()
		instructions, hasInstructions := stringField(r.input(), "instructions")
		contextJSON, err := history.AppendSummaryContext(nil, view.SummarizedMessagesBefore(cut), []byte(instructions), hasInstructions, now)
		if err != nil {
			r.fail(err)
			return
		}
		r.provider(func(sessionID string, err error) {
			if err != nil {
				r.fail(err)
				return
			}
			thinking := str(cp, "thinkingLevel")
			optionsJSON, err := history.AppendSummaryOptions(nil, []byte(jv.Stringify(streamOrEmpty(objField(cp, "streamOptions")))), num(cp, "maxTokens"),
				[]byte(sessionID), []byte(jv.Stringify(thinking)))
			if err != nil {
				r.fail(err)
				return
			}
			req := &CompleteRequest{Model: ref, Context: parseJSONObject(contextJSON), Options: parseJSONObject(optionsJSON)}
			h.Complete(req, r.rt.Signal(), func(message *jv.Object, err error) {
				if err != nil {
					r.fail(err)
					return
				}
				// An abort mark or close: the abort invocation or the reopened task handles the committed state.
				if r.rt.Signal().Aborted() {
					r.fail(session.ErrAborted)
					return
				}
				r.settleSummary(cp, attempt, message)
			})
		})
	})
}

func (r *compactionRun) settleSummary(cp *jv.Object, attempt float64, message *jv.Object) {
	h := r.g.Host
	settings := h.Settings()
	raw := []byte(jv.Stringify(message))
	outcome := history.DecideSummary(raw, int64(attempt), history.RetryPolicy{
		Enabled: settings.RetryEnabled, MaxRetries: int64(settings.MaxRetries),
		Retryable: IsRetryableAssistantError(message),
		DelayMs:   RetryDelayMs(settings.BaseDelayMs, settings.MaxAgentDelayMs, attempt),
	}, r.now())
	firstKept := idField(cp, "firstKept")
	r.commitDone(func(tx *session.Tx, current *jv.Object) (*jv.Object, error) {
		if usage := objField(message, "usage"); usage != nil {
			if err := recordUsageTx(tx, r.conv, UsageModels, str(message, "provider")+"/"+str(message, "model"), usage); err != nil {
				return nil, err
			}
		}
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		switch outcome.Decision {
		case history.SummaryPlace:
			return r.placeSummary(tx, current, live, firstKept, string(outcome.Summary))
		case history.SummaryRetry:
			if status := CompactionStatusOf(live, r.rt.TaskID()); status != nil {
				if err := status.Set("retry", jobj("at", outcome.Until, "error", str(message, "errorMessage"))); err != nil {
					return nil, err
				}
			}
			next := cp.Shallow()
			next.Set("phase", phaseRetry)
			next.Set("until", outcome.Until)
			return jobj("status", "sleeping", "checkpoint", next, "until", outcome.Until), nil
		}
		if err := FailCompactionStatus(live, r.rt.TaskID(), string(outcome.FailText)); err != nil {
			return nil, err
		}
		return jobj("status", "terminal", "outcome", modelErrorOutcome(string(outcome.FailText))), nil
	})
}

// retry sleeps until the backoff ends, then summarizes again.
func (r *compactionRun) retry(cp *jv.Object) {
	attempt := num(cp, "attempt") + 1
	// After the backoff's sleep; a run before until (an early wake, or a record of an earlier version) sleeps again.
	if until := num(cp, "until"); until > r.now() {
		r.rt.SleepUntil(until, cp, func(err error) { r.done(err) })
		return
	}
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		if status := CompactionStatusOf(live, r.rt.TaskID()); status != nil {
			if err := status.Set("attempt", attempt); err != nil {
				return nil, err
			}
			status.Delete("retry")
		}
		next := cp.Shallow()
		next.Delete("until")
		next.Set("phase", phaseSummarize)
		next.Set("attempt", attempt)
		return jobj("status", "running", "checkpoint", next), nil
	})
}

// place places a summary supplied by a hook in its own commit.
func (r *compactionRun) place(firstKept ID, summary string) {
	r.commitDone(func(tx *session.Tx, current *jv.Object) (*jv.Object, error) {
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		return r.placeSummary(tx, current, live, firstKept, summary)
	})
}

// placeSummary places the summary entry and completes. A blocking compaction, owned by its generation, appends it: the
// generation holds the run and waits. A conversation-owned one admits it as a write submission, placed at once when idle,
// otherwise at the next boundary, or settled stale. Nothing else may append to a busy conversation.
func (r *compactionRun) placeSummary(tx *session.Tx, current *jv.Object, live *deltaObj, firstKept ID, summary string) (*jv.Object, error) {
	if err := RemoveCompactionStatus(live, r.rt.TaskID()); err != nil {
		return nil, err
	}
	live.Delete("compactionFailure")
	reason := str(objField(current, "input"), "reason")
	draftJSON, err := history.AppendSummaryEntryDraft(nil, int64(firstKept), []byte(summary), reason, r.now())
	if err != nil {
		return nil, err
	}
	entry := parseJSONObject(draftJSON)
	var result *jv.Object
	if _, owned := current.Get("owner"); !owned {
		requestID := string(history.CompactionRequestID(int64(r.rt.TaskID())))
		id, err := inbox.Admit(tx, Runs{}, r.conv, inbox.Draft{RequestID: &requestID, Type: "write", Entry: entry}, r.now(), r.g.Host.Settings().Queue)
		if err != nil {
			return nil, err
		}
		result = jobj("submissionId", float64(id))
	} else {
		record, err := tx.AppendEntry(r.conv, entry)
		if err != nil {
			return nil, err
		}
		result = jobj("entryId", float64(idField(record, "id")))
	}
	return jobj("status", "terminal", "outcome", jobj("status", "completed", "result", result)), nil
}

// abort removes the status and ends the task aborted.
func (g *CompactionTask) abort(rt *session.Runtime, task *jv.Object, done func(error)) {
	r := g.newRun(rt, task, done)
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		if _, err := r.removeStatus(tx); err != nil {
			return nil, err
		}
		return jobj("status", "terminal", "outcome", jobj("status", "aborted")), nil
	})
}

type deltaObj = delta.Obj
