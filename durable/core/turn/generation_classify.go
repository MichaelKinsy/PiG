// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"math"

	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

func withCompacted(cp *jv.Object, compacted ID) {
	if compacted != 0 {
		cp.Set("compacted", float64(compacted))
	}
}

// classify classifies a terminal provider message in one commit that also clears the partial.
func (r *run) classify(req requestInfo, message *jv.Object) {
	h := r.g.Host
	if r.rt.Signal().Aborted() {
		// An abort mark or close: the abort invocation or the reopened run handles the committed state.
		r.fail(session.ErrAborted)
		return
	}
	stop := str(message, "stopReason")
	if handle := objField(message, "deferred"); stop == "deferred" && handle != nil {
		after := float64(defaultPollAfterMs)
		if v, ok := handle.Get("pollAfterMs"); ok {
			if f, isNum := v.(float64); isNum {
				after = f
			}
		}
		pollAt := r.now() + after
		if req.pollAt != nil {
			pollAt = math.Max(pollAt, *req.pollAt+1)
		}
		r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
			live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
			if err != nil {
				return nil, err
			}
			if err := live.Set("generation", jobj("attempt", req.attempt, "deferred", jobj("pollAt", pollAt))); err != nil {
				return nil, err
			}
			cp := jobj("phase", phasePoll, "attempt", req.attempt)
			withCompacted(cp, req.compacted)
			cp.Set("model", req.model.object())
			cp.Set("cutoff", float64(req.cutoff))
			cp.Set("handle", handle)
			cp.Set("pollAt", pollAt)
			return jobj("status", "sleeping", "checkpoint", cp, "until", pollAt), nil
		})
		return
	}
	r.eachHook("afterResponse", func() *jv.Object { return jobj("message", message) }, func(any) bool { return false }, func(err error) {
		if err != nil {
			r.fail(err)
			return
		}
		var calls []*jv.Object
		content, _ := arrayField(message, "content")
		for _, c := range content {
			if o, ok := c.(*jv.Object); ok && str(o, "type") == "toolCall" {
				calls = append(calls, o)
			}
		}
		switch {
		case stop == "toolUse" && len(calls) > 0:
			r.startToolRound(req, message, calls)
		case stop == "stop" || stop == "length" || stop == "toolUse":
			r.answer(message)
		default:
			r.classifyFailure(req, message, h.Settings())
		}
	})
}

func (r *run) classifyFailure(req requestInfo, message *jv.Object, settings Settings) {
	stop := str(message, "stopReason")
	overflow := stop == "error" && IsContextOverflow(message)
	compaction := settings.CompactionFor(req.model)
	if !overflow || req.compacted != 0 || !compaction.Enabled {
		r.retryOrFail(req, message, settings, stop, overflow)
		return
	}
	r.context(req.cutoff, func(view *history.View) {
		if _, ok := view.SelectCut(compaction.KeepRecentTokens); ok {
			text := "Context overflow"
			if s, ok := stringField(message, "errorMessage"); ok {
				text = s
			}
			r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
				live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
				if err != nil {
					return nil, err
				}
				if _, err := appendAssistant(tx, r.conv, message); err != nil {
					return nil, err
				}
				live.Delete("generation")
				child, err := createCompaction(tx, r.conv, "overflow", r.rt.TaskID())
				if err != nil {
					return nil, err
				}
				cp := jobj("phase", phasePrepare, "attempt", req.attempt, "compacted", float64(child), "overflow", text)
				return jobj("status", "waiting", "checkpoint", cp, "on", idList([]ID{child}), "policy", "allSettled"), nil
			})
			return
		}
		r.retryOrFail(req, message, settings, stop, overflow)
	})
}

// retryOrFail commits a failed answer: a retry with its backoff, or the terminal failure.
func (r *run) retryOrFail(req requestInfo, message *jv.Object, settings Settings, stop string, overflow bool) {
	// An overflow is never retried: only a compaction can make the next request fit.
	retry := stop == "error" && !overflow && IsRetryableAssistantError(message) && settings.RetryEnabled && req.attempt <= settings.MaxRetries
	until := 0.0
	if retry {
		until = r.now() + RetryDelayMs(settings.BaseDelayMs, settings.MaxAgentDelayMs, req.attempt)
	}
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
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
		if _, err := appendAssistant(tx, r.conv, message); err != nil {
			return nil, err
		}
		if retry {
			if err := live.Set("generation", jobj("attempt", req.attempt, "retry", jobj("at", until, "error", str(message, "errorMessage")))); err != nil {
				return nil, err
			}
			cp := jobj("phase", phaseRetry, "attempt", req.attempt)
			withCompacted(cp, req.compacted)
			cp.Set("until", until)
			return jobj("status", "sleeping", "checkpoint", cp, "until", until), nil
		}
		text, ok := stringField(message, "errorMessage")
		if !ok {
			text = "Model response ended with stop reason " + stop
		}
		if err := r.endFailedRun(tx, live, failed, jobj("status", "unanswered", "reason", "model_error", "detail", text)); err != nil {
			return nil, err
		}
		return jobj("status", "terminal", "outcome", modelErrorOutcome(text)), nil
	})
}

// failure is what a failed run's end needs from the table, read before the commit's first table write (generation.ts Failure).
type failure struct {
	policy   string
	boundary *inbox.Boundary
}

// prepareFailure reads the boundary a failed run's end may apply, only under the continue policy (generation.ts
// prepareFailure, an async function its caller awaits).
func (r *run) prepareFailure(tx *session.Tx) (failure, error) {
	settings := r.g.Host.Settings()
	policy := settings.AfterFailedRun
	if policy != "continue" {
		tx.Await(1)
		return failure{policy: policy}, nil
	}
	boundary, err := prepareBoundary(tx, r.conv, settings.Queue)
	if err != nil {
		return failure{}, err
	}
	tx.Await(1)
	return failure{policy: policy, boundary: boundary}, nil
}

// endFailedRun ends a failed run, settling its inputs with settlement, and does with the inputs queued behind it what
// settings.afterFailedRun says (spec section 6): keep them, start the next run with them, or withdraw them (generation.ts
// endFailedRun, an async function its caller awaits).
func (r *run) endFailedRun(tx *session.Tx, live *delta.Obj, failed failure, settlement *jv.Object) error {
	if err := EndRun(tx, live, r.rt.TaskID(), settlement); err != nil {
		return err
	}
	if failed.policy == "withdraw" {
		if err := inbox.WithdrawQueuedFor(tx, r.conv, "run_failed"); err != nil {
			return err
		}
		tx.Await(1)
	}
	if failed.boundary != nil {
		applied, err := applyBoundary(tx, failed.boundary, true, r.now())
		if err != nil {
			return err
		}
		if len(applied.Users) > 0 {
			if err := awaitStartRun(tx, r.conv, live, applied.Users); err != nil {
				return err
			}
		}
	}
	tx.Await(1)
	return nil
}

// answer is a final answer; the final boundary places queued items (spec section 6). The first onYield continuation
// appends a user message and hands the run to a successor generation, but only when the boundary selected no user item
// and no reset. Otherwise the run's inputs settle done, and selected user items start the next run.
func (r *run) answer(message *jv.Object) {
	var continuation any
	r.eachHook("onYield", func() *jv.Object { return jobj("answer", message) }, func(result any) bool {
		if o, ok := result.(*jv.Object); ok {
			if c, has := o.Get("continue"); has && c != nil {
				continuation = c
				return true
			}
		}
		return false
	}, func(err error) {
		if err != nil {
			r.fail(err)
			return
		}
		r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
			// Queue modes are read on the Session line, when the boundary is decided.
			boundary, err := prepareBoundary(tx, r.conv, r.g.Host.Settings().Queue)
			if err != nil {
				return nil, err
			}
			live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
			if err != nil {
				return nil, err
			}
			entry, err := appendAssistant(tx, r.conv, message)
			if err != nil {
				return nil, err
			}
			entryID := idField(entry, "id")
			result := jobj("status", "terminal", "outcome", jobj("status", "completed", "result", jobj("entryId", float64(entryID))))
			applied, err := applyBoundary(tx, boundary, true, r.now())
			if err != nil {
				return nil, err
			}
			if continuation != nil && len(applied.Users) == 0 && !applied.Reset {
				user := jobj("role", "user", "content", continuation, "timestamp", r.now())
				if _, err := tx.AppendEntry(r.conv, entryDraft(KindUser, jobj("model", []any{user}))); err != nil {
					return nil, err
				}
				next, err := createGeneration(tx, r.conv)
				if err != nil {
					return nil, err
				}
				handOver(live, r.rt.TaskID(), next)
				live.Delete("generation")
				return result, nil
			}
			if err := EndRun(tx, live, r.rt.TaskID(), jobj("status", "done", "answer", float64(entryID))); err != nil {
				return nil, err
			}
			if len(applied.Users) > 0 {
				if err := awaitStartRun(tx, r.conv, live, applied.Users); err != nil {
					return nil, err
				}
			}
			return result, nil
		})
	})
}

// createGeneration creates a generation owned by its conversation.
func createGeneration(tx *session.Tx, conv ID) (ID, error) {
	return tx.CreateTask(GenerationDef, jv.NewObject(), session.TaskOptions{ConversationID: conv})
}

// startRun starts a run for inputs, placed input submissions: a new generation takes pi.live.run.
func startRun(tx *session.Tx, conv ID, live *delta.Obj, inputs []ID) error {
	id, err := createGeneration(tx, conv)
	if err != nil {
		return err
	}
	return live.Set("run", jobj("taskId", float64(id), "inputs", idList(inputs)))
}

// handOver hands run control from one task to another; the run's inputs move with it.
func handOver(live *delta.Obj, from, to ID) {
	if run := live.Obj("run"); run != nil && run.Get("taskId") == float64(from) {
		_ = run.Set("taskId", float64(to))
	}
}

// Runs implements inbox.Runs: the conversation's pi.live document and the start of a run for placed inputs.
type Runs struct{}

// Live returns the pi.live draft of a conversation.
func (Runs) Live(tx *session.Tx, conv ID) (*delta.Obj, error) {
	return tx.Doc(LiveDef, session.DocArgs{Owner: conv})
}

// Start is startRun.
func (Runs) Start(tx *session.Tx, conv ID, live *delta.Obj, inputs []ID) error {
	return startRun(tx, conv, live, inputs)
}

func createToolTask(tx *session.Tx, owner, assistant ID, callID string) (ID, error) {
	return tx.CreateTask(ToolDef, jobj("kind", "model", "assistant", float64(assistant), "callId", callID), session.TaskOptions{OwnerTask: owner})
}

// startToolRound appends the tool-calling answer and starts its tool round in one commit (spec section 8.3). A call to a
// tool the request did not offer gets its tool_unavailable result here; every other call gets a tool task owned by the
// generation, only the first one now when the round is sequential. The generation then waits for them in its tools
// phase, keeping the run.
func (r *run) startToolRound(req requestInfo, message *jv.Object, calls []*jv.Object) {
	if req.messages != nil {
		r.startToolRoundWith(req.messages, message, calls)
		return
	}
	r.requestContext(req.cutoff, func(view RequestContext) { r.startToolRoundWith(view.SystemMessages(), message, calls) })
}

func (r *run) startToolRoundWith(messages []*jv.Object, message *jv.Object, calls []*jv.Object) {
	offered := map[string]bool{}
	for _, t := range CurrentTools(messages) {
		offered[nameOf(t)] = true
	}
	// Read as the round starts; a tool is resolved as its tool task resolves it.
	r.agent(func(agent *Agent) { r.startRoundWith(message, calls, offered, agent) })
}

func (r *run) startRoundWith(message *jv.Object, calls []*jv.Object, offered map[string]bool, agent *Agent) {
	h := r.g.Host
	sequential := h.Settings().ToolExecution == "sequential"
	for _, call := range calls {
		name := str(call, "name")
		if !offered[name] {
			continue
		}
		for _, t := range agent.Tools {
			if nameOf(t.Declaration) == name {
				if t.ExecutionMode == "sequential" {
					sequential = true
				}
				break
			}
		}
	}
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		entry, err := appendAssistant(tx, r.conv, message)
		if err != nil {
			return nil, err
		}
		assistant := idField(entry, "id")
		var slots, started []any
		var tools []ID
		pending := []any{}
		for _, call := range calls {
			id, name := str(call, "id"), str(call, "name")
			if !offered[name] {
				result, err := AppendToolResult(tx, r.conv, call, HarnessError("tool_unavailable", "Tool "+name+" is not available"), r.now(), nil)
				if err != nil {
					return nil, err
				}
				slots = append(slots, jobj("callId", id, "name", name, "status", "done", "entry", float64(idField(result, "id"))))
				continue
			}
			if sequential && len(tools) > 0 {
				pending = append(pending, id)
				slots = append(slots, jobj("callId", id, "name", name, "status", "pending"))
				continue
			}
			taskID, err := createToolTask(tx, r.rt.TaskID(), assistant, id)
			if err != nil {
				return nil, err
			}
			tools = append(tools, taskID)
			started = append(started, float64(taskID))
			slots = append(slots, jobj("callId", id, "name", name, "taskId", float64(taskID), "status", "pending"))
		}
		live.Delete("generation")
		if slots == nil {
			slots = []any{}
		}
		if err := live.Set("tools", slots); err != nil {
			return nil, err
		}
		if started == nil {
			started = []any{}
		}
		cp := jobj("phase", phaseTools, "assistant", float64(assistant), "tools", started, "pending", pending)
		return jobj("status", "waiting", "checkpoint", cp, "on", idList(tools), "policy", "allSettled"), nil
	})
}
