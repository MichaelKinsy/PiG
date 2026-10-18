// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/inbox"
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// The built-in pi.generation task (src/harness/generation.ts): it prepares the positional system prompt and tool
// loadout, requests or polls the model, retries, and classifies the response. The run's inputs live in pi.live.run. Each
// `await` of the TypeScript is a continuation; a continuation that Pi runs after a commit runs after the commit's job.

const defaultPollAfterMs = 5000

// Generation phases.
const (
	phasePrepare = "prepare"
	phaseRequest = "request"
	phaseRetry   = "retry"
	phasePoll    = "poll"
	phaseTools   = "tools"
)

// GenerationDef is the pi.generation definition: first checkpoint {phase: "prepare", attempt: 1}.
// Read-only: assigned once at initialization.
var GenerationDef = &session.TaskDef{
	Name:    KindGeneration,
	Version: 1,
	Initial: func(any) any { return jobj("phase", phasePrepare, "attempt", 1.0) },
}

// jobj builds an ordered object from alternating keys and values.
func jobj(kv ...any) *jv.Object {
	o := jv.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func num(o *jv.Object, key string) float64 {
	if o == nil {
		return 0
	}
	v, _ := o.Get(key)
	f, _ := v.(float64)
	return f
}

func idField(o *jv.Object, key string) ID { return ID(num(o, key)) }

func objField(o *jv.Object, key string) *jv.Object {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	r, _ := v.(*jv.Object)
	return r
}

func str(o *jv.Object, key string) string {
	s, _ := stringField(o, key)
	return s
}

func checkpointOf(task *jv.Object) *jv.Object { return objField(objField(task, "state"), "checkpoint") }

func idList(ids []ID) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = float64(id)
	}
	return out
}

func modelRefOf(o *jv.Object) ModelRef {
	return ModelRef{Provider: str(o, "provider"), ModelID: str(o, "modelId")}
}

func (m ModelRef) object() *jv.Object { return jobj("provider", m.Provider, "modelId", m.ModelID) }

// abortReturnHops is the hops from the end of an abort handler's final commit job to runAbort's step: `#gated` resumes from
// commitWith, the handler's `await runtime.commit()` resumes, the async handler returns, and `await abort()` resumes
// (scheduler.ts #runAbort, generation.ts and compaction.ts abort). Evidence pins "not at once" (E23 is red at 0 and equal at 1 to 5: the
// step's enqueue shows only as a line order against a host call of the same pass, and no harness-visible tick count includes it: a
// F-ticks row of abort-until-idle is the same at 0 to 6); the 3 is the reading of the code. The tool task's abort awaits `settle`, one resumption more;
// the core does not charge it yet: a charge of even one hop hangs the U-harness-ownership test "rejects a handle operation queued on the
// line when the invocation ends before it runs".
const abortReturnHops = 3

// Generation implements the pi.generation task over a Host.
type Generation struct{ Host *Host }

// Impl returns the executable definition registered in the registry.
func (g *Generation) Impl() *session.TaskImpl {
	return &session.TaskImpl{Def: *GenerationDef, Phase: g.phase, Abort: g.abort, AbortReturnHops: abortReturnHops}
}

// run carries what the phases of one invocation share.
type run struct {
	// kind is the task kind of the invocation, which selects the hooks that apply.
	kind string
	g    *Generation
	rt   *session.Runtime
	conv ID
	done func(error)
	// agentSeen: the agent was resolved in this invocation, as phaseAgent. Pi memoizes runtime.agent() per phase (scheduler.ts
	// #runtime agent(): phase.agent) and hooks.each awaits it, so a phase that reaches its hooks first (a recovered tools
	// phase) resolves the agent before it counts handlers, and a later runtime.agent() of the phase reuses the resolution.
	agentSeen  bool
	phaseAgent *Agent
	// hookIndex is the index of the hook handler eachHookRaw is calling, which names the extension of a failure.
	hookIndex int
}

func (g *Generation) newRun(rt *session.Runtime, done func(error)) *run {
	return &run{kind: KindGeneration, g: g, rt: rt, conv: rt.ConversationID(), done: done}
}

func (r *run) now() float64 { return r.g.Host.Now() }

// fail ends the phase with err.
func (r *run) fail(err error) { r.done(err) }

func (g *Generation) phase(rt *session.Runtime, task *jv.Object, done func(error)) {
	r := g.newRun(rt, done)
	cp := checkpointOf(task)
	switch str(cp, "phase") {
	case phasePrepare:
		r.prepare(cp)
	case phaseRequest:
		r.request(cp)
	case phaseRetry:
		r.retry(cp)
	case phasePoll:
		r.poll(cp)
	case phaseTools:
		r.tools(cp)
	default:
		done(errors.New("Generation phase " + str(cp, "phase") + " is unknown"))
	}
}

// commitState runs change in a task commit and ends the phase.
func (r *run) commit(change func(tx *session.Tx, current *jv.Object) (*jv.Object, error), then func()) {
	r.rt.Commit(change, func(err error) {
		if err != nil {
			r.fail(err)
			return
		}
		then()
	})
}

func (r *run) commitDone(change func(tx *session.Tx, current *jv.Object) (*jv.Object, error)) {
	r.commit(change, func() { r.done(nil) })
}

// promptView adapts a context view for prompt planning.
func promptView(v *history.View) PromptView {
	pv := PromptView{HasHead: v.HasHead(), HeadID: v.HeadID()}
	// Only a context with a head marker reads its entries (PlanSystemEntries), so a long transcript's entries are not
	// listed on every request.
	if pv.HasHead {
		pv.Entries = make([]PromptEntry, v.NumEntries())
		for i := range pv.Entries {
			pv.Entries[i] = PromptEntry{ID: v.EntryID(i), Kind: v.EntryKindString(i)}
		}
	}
	pv.Messages = systemMessagesOf(v)
	return pv
}

// messagesOf parses the model messages of a view.
func messagesOf(v *history.View) []*jv.Object {
	out := make([]*jv.Object, 0, v.NumMessages())
	for i := 0; i < v.NumMessages(); i++ {
		if m, err := jv.Parse(v.Message(i)); err == nil {
			if o, ok := m.(*jv.Object); ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// systemMessagesOf parses the system messages of a view, in order. The readers of a context's sections and tools
// (ReplaySections, CurrentTools: promptView.Messages, the offered tools of a round) look only at those, so the other
// messages, whose tool outputs are the bulk of a context, are not parsed.
func systemMessagesOf(v *history.View) []*jv.Object {
	var out []*jv.Object
	for j := 0; j < v.NumSystemMessages(); j++ {
		if m, err := jv.Parse(v.SystemMessage(j)); err == nil {
			if o, ok := m.(*jv.Object); ok {
				out = append(out, o)
			}
		}
	}
	return out
}

func modelErrorOutcome(text string) *jv.Object {
	return jobj("status", "failed", "error", jobj("message", text, "detail", jobj("reason", "model_error")))
}

// failModelError settles the run's inputs unanswered with model_error and fails with text.
func (r *run) failModelError(text string) {
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		failed, err := r.prepareFailure(tx)
		if err != nil {
			return nil, err
		}
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		if err := r.endFailedRun(tx, live, failed, jobj("status", "unanswered", "reason", "model_error", "detail", text)); err != nil {
			return nil, err
		}
		return jobj("status", "terminal", "outcome", modelErrorOutcome(text)), nil
	})
}

// failNoModel settles the run's inputs unanswered with no_model and fails.
func (r *run) failNoModel(ref *ModelRef) {
	message := "No model is configured"
	if ref != nil {
		message = "Model " + ref.Provider + "/" + ref.ModelID + " is not available"
	}
	r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		failed, err := r.prepareFailure(tx)
		if err != nil {
			return nil, err
		}
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		if err := r.endFailedRun(tx, live, failed, jobj("status", "unanswered", "reason", "no_model")); err != nil {
			return nil, err
		}
		return jobj("status", "terminal", "outcome", jobj("status", "failed", "error", jobj("message", message, "detail", jobj("reason", "no_model")))), nil
	})
}

// appendAssistant appends a provider result and adds its usage to pi.usage in the same commit: every built-in writer of
// assistant entries goes through here, so the usage ledger stays complete.
func appendAssistant(tx *session.Tx, conv ID, message *jv.Object) (*jv.Object, error) {
	if usage := objField(message, "usage"); usage != nil {
		if err := recordUsageTx(tx, conv, UsageModels, str(message, "provider")+"/"+str(message, "model"), usage); err != nil {
			return nil, err
		}
	}
	entry, err := tx.AppendEntry(conv, entryDraft(KindAssistant, jobj("model", []any{message})))
	if err != nil {
		return nil, err
	}
	// generation.ts :654-662: appendAssistant is an async function that awaits recordUsage and returns tx.appendEntry's promise, and every
	// caller awaits it: the caller resumes one tick after it returns.
	tx.Await(1)
	return entry, nil
}

// prepareBoundary is `await prepareBoundary(...)` (generation.ts :522, :621): the caller resumes one tick after the async function returns.
func prepareBoundary(tx *session.Tx, conv ID, modes inbox.QueueModes) (*inbox.Boundary, error) {
	boundary, err := inbox.Prepare(tx, conv, modes)
	if err == nil {
		tx.Await(1)
	}
	return boundary, err
}

// applyBoundary is `await applyBoundary(...)` (generation.ts :526, :631, :635): the caller resumes one tick after the async function returns.
func applyBoundary(tx *session.Tx, boundary *inbox.Boundary, final bool, now float64) (inbox.Result, error) {
	applied, err := inbox.Apply(tx, boundary, final, now)
	if err == nil {
		tx.Await(1)
	}
	return applied, err
}

// awaitStartRun is `await startRun(...)` (generation.ts :535, :633, :639): the caller resumes one tick after the async function returns.
func awaitStartRun(tx *session.Tx, conv ID, live *delta.Obj, inputs []ID) error {
	if err := startRun(tx, conv, live, inputs); err != nil {
		return err
	}
	tx.Await(1)
	return nil
}

// convertPartial appends a committed partial left by an interrupted, aborted, faulted or orphaned attempt as an aborted
// assistant entry; the caller replaces or removes `generation`.
func convertPartial(tx *session.Tx, live *delta.Obj, conv ID) error {
	generation := live.Obj("generation")
	if generation == nil || !generation.Has("message") {
		tx.Await(1) // generation.ts convertPartial is async: nothing to append still costs the caller's await one tick
		return nil
	}
	snapshot := generation.Snapshot()
	m, _ := snapshot.Get("message")
	partial, ok := m.(*jv.Object)
	if !ok {
		tx.Await(1)
		return nil
	}
	message := jv.Clone(partial).(*jv.Object)
	message.Set("stopReason", "aborted")
	_, err := appendAssistant(tx, conv, message)
	return err
}

// createCompaction creates a compaction task with its status in this commit. owner is the generation that waits for it (a
// blocking compaction); without one it is conversation-owned, and background unless it is manual.
func createCompaction(tx *session.Tx, conv ID, reason string, owner ID) (ID, error) {
	return CreateCompaction(tx, conv, jobj("reason", reason), owner)
}

// CreateCompaction is compaction.ts createCompaction for an input {reason, instructions?}: Conversation.compact uses it
// with reason "manual".
func CreateCompaction(tx *session.Tx, conv ID, input *jv.Object, owner ID) (ID, error) {
	reason := str(input, "reason")
	taskID, err := tx.CreateTask(CompactionDef, input, session.TaskOptions{OwnerTask: owner, ConversationID: conv, Background: owner == 0 && reason != "manual"})
	if err != nil {
		return 0, err
	}
	live, err := tx.Doc(LiveDef, session.DocArgs{Owner: conv})
	if err != nil {
		return 0, err
	}
	status := jobj("taskId", float64(taskID), "reason", reason, "blocking", owner != 0, "attempt", 1.0)
	return taskID, AddCompactionStatus(live, status)
}

// CompactionDef is the pi.compaction definition: first checkpoint {phase: "select"} (compaction.ts).
// Read-only: assigned once at initialization.
var CompactionDef = &session.TaskDef{
	Name:    KindCompaction,
	Version: 1,
	Initial: func(any) any { return jobj("phase", "select") },
}

// ProviderDef is ProviderDoc: the provider-facing identity of one conversation; every fork starts with a fresh one.
// Read-only: assigned once at initialization.
var ProviderDef = &session.DocDef{
	Kind:           "pi.provider",
	Version:        1,
	Scope:          session.ScopeConversation,
	History:        session.HistoryLatest,
	Fork:           session.ForkInitial,
	Initial:        func(any) *jv.Object { return jobj("sessionId", "") },
	CheckpointWhen: func(*jv.Object, []delta.Op, int) bool { return true },
}

// ---- prepare ---------------------------------------------------------------------------------------------------

func (r *run) prepare(cp *jv.Object) {
	r.agent(func(agent *Agent) { r.prepareWith(cp, agent) })
}

func (r *run) prepareWith(cp *jv.Object, agent *Agent) {
	h := r.g.Host
	settings := h.Settings()
	var window float64
	known := false
	if agent.Model != nil {
		window, known = h.ContextWindow(*agent.Model)
	}
	if agent.Model == nil || !known {
		r.failNoModel(agent.Model)
		return
	}
	attempt := num(cp, "attempt")
	compacted := idField(cp, "compacted")
	overflowText, hasOverflow := stringField(cp, "overflow")
	proceed := func() { r.prepareRequest(agent, settings, window, attempt, compacted) }
	if compacted != 0 && hasOverflow {
		r.rt.Outcomes([]ID{compacted}, func(outcomes []*jv.Object, err error) {
			if err != nil {
				r.fail(err)
				return
			}
			var outcome *jv.Object
			if len(outcomes) > 0 {
				outcome = outcomes[0]
			}
			if str(outcome, "status") != "completed" || objField(outcome, "result") == nil || !objField(outcome, "result").Has("entryId") {
				r.failModelError(overflowText)
				return
			}
			proceed()
		})
		return
	}
	// A threshold compaction that found nothing to compact or was declined leaves a request that may still fit. One that failed
	// left a context it judged too large: sending it would at best leave no room for the answer.
	if threshold, _ := cp.Get("threshold"); compacted != 0 && threshold == true {
		r.rt.Outcomes([]ID{compacted}, func(outcomes []*jv.Object, err error) {
			if err != nil {
				r.fail(err)
				return
			}
			if len(outcomes) > 0 && outcomes[0] != nil && str(outcomes[0], "status") != "completed" {
				r.failModelError(compactionFailureText(outcomes[0]))
				return
			}
			proceed()
		})
		return
	}
	proceed()
}

// compactionFailureText is the error text of a generation whose blocking compaction ended without completing.
func compactionFailureText(outcome *jv.Object) string {
	switch str(outcome, "status") {
	case "failed", "faulted":
		return "Compaction failed: " + str(objField(outcome, "error"), "message")
	case "orphaned":
		return "Compaction failed: " + str(outcome, "reason")
	}
	return "Compaction was aborted"
}

func (r *run) prepareRequest(agent *Agent, settings Settings, window, attempt float64, compacted ID) {
	h := r.g.Host
	r.requestContext(0, func(view RequestContext) {
		pv := view.PromptView()
		shown := ReplaySections(pv.Messages)
		h.Render(r.conv, r.rt.TaskID(), agent, shown, r.rt.Signal(), func(desired *Sections, err error) {
			if err != nil {
				r.fail(err)
				return
			}
			entries := PlanSystemEntries(pv, desired, agent.Declarations(), r.now())
			threshold := history.ThresholdNone
			if compacted == 0 {
				planned := make([][]byte, 0, len(entries))
				for _, e := range entries {
					if model, ok := e.Get("model"); ok {
						for _, m := range model.([]any) {
							planned = append(planned, []byte(jv.Stringify(m)))
						}
					}
				}
				threshold = view.Threshold(planned, window, settings.CompactionFor(*agent.Model))
			}
			if threshold == history.ThresholdBlocking {
				// Compact first and prepare again; the transcript is unchanged until the compaction appends.
				r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
					child, err := createCompaction(tx, r.conv, "threshold", r.rt.TaskID())
					if err != nil {
						return nil, err
					}
					checkpoint := jobj("phase", phasePrepare, "attempt", attempt, "compacted", float64(child), "threshold", true)
					return jobj("status", "waiting", "checkpoint", checkpoint, "on", idList([]ID{child}), "policy", "allSettled"), nil
				})
				return
			}
			r.commitDone(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
				page, err := tx.ScanEntries(session.EntryQuery{ConversationID: r.conv}, 1, 0)
				if err != nil {
					return nil, err
				}
				var cutoff ID
				if len(page.Items) > 0 {
					cutoff = idField(page.Items[0], "id")
				}
				for _, e := range entries {
					rec, err := tx.AppendEntry(r.conv, entryDraft(KindSystem, e))
					if err != nil {
						return nil, err
					}
					cutoff = idField(rec, "id")
				}
				if cutoff == 0 {
					return nil, errors.New("Conversation " + itoa(int64(r.conv)) + " has no entries to send")
				}
				// Checked in this commit, so a compaction admitted during preparation counts.
				if threshold == history.ThresholdBackground {
					live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
					if err != nil {
						return nil, err
					}
					if !live.Has("compactions") {
						if _, err := createCompaction(tx, r.conv, "threshold", 0); err != nil {
							return nil, err
						}
					}
				}
				cp := jobj("phase", phaseRequest, "attempt", attempt)
				if compacted != 0 {
					cp.Set("compacted", float64(compacted))
				}
				cp.Set("model", agent.Model.object())
				cp.Set("thinkingLevel", agent.ThinkingLevel)
				cp.Set("streamOptions", streamOrEmpty(settings.Stream))
				cp.Set("cutoff", float64(cutoff))
				return jobj("status", "running", "checkpoint", cp), nil
			})
		})
	})
}

func streamOrEmpty(o *jv.Object) *jv.Object {
	if o == nil {
		return jv.NewObject()
	}
	return jv.Clone(o).(*jv.Object)
}

// itoa formats an integer without fmt or strconv.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	u := uint64(n)
	if neg {
		u = -u
	}
	var b [20]byte
	i := len(b)
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Machines returns the executable definitions of the built-in tasks over a Host, in registration order, for the registry
// snapshot of the Scheduler. Runs{} implements inbox.Runs for admission.
func Machines(h *Host) []*session.TaskImpl {
	return []*session.TaskImpl{(&Generation{Host: h}).Impl(), (&ToolTask{Host: h}).Impl(), (&CompactionTask{Host: h}).Impl()}
}
