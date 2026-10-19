// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"errors"
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// The built-in pi.tool task (src/harness/tool.ts): it resolves the called tool among its phase agent's tools, validates,
// runs beforeTool, records intent, executes, runs afterTool and appends the result, all in one `call` phase so nothing
// separates resolution from settlement. `execute` is reached only by recovery and applies the replay rule.

const (
	phaseCall    = "call"
	phaseExecute = "execute"

	defaultMaxBytes = 50 * 1024
	defaultMaxLines = 2000
)

// ToolTask implements the pi.tool task over a Host.
type ToolTask struct{ Host *Host }

// Impl returns the executable definition registered in the registry.
func (g *ToolTask) Impl() *session.TaskImpl {
	return &session.TaskImpl{Def: *ToolDef, Migrate: migrateToolTask, Phase: g.phase, Abort: g.abort}
}

type toolRun struct {
	*run
	input *jv.Object
}

func (g *ToolTask) newRun(rt *session.Runtime, task *jv.Object, done func(error)) *toolRun {
	gen := &Generation{Host: g.Host}
	return &toolRun{run: gen.newRun(rt, done), input: objField(task, "input")}
}

func (g *ToolTask) phase(rt *session.Runtime, task *jv.Object, done func(error)) {
	r := g.newRun(rt, task, done)
	cp := checkpointOf(task)
	switch str(cp, "phase") {
	case phaseCall:
		r.call()
	case phaseExecute:
		r.execute(cp)
	default:
		done(errors.New("Tool phase " + str(cp, "phase") + " is unknown"))
	}
}

// readCall reads the tool call callId of the assistant entry.
func (r *toolRun) readCall(then func(call *jv.Object, ok bool)) {
	// A nested call carries its own call; no entry holds it.
	if r.isNested() {
		if call := objField(r.input, "call"); call != nil {
			then(call, true)
			return
		}
		r.fail(errors.New("Nested call " + str(r.input, "key") + " has no call"))
		return
	}
	r.entry(idField(r.input, "assistant"), func(entry *jv.Object) {
		// `#read` returns the arrow's promise from an async function (adoption, 2), runtime.entry's awaiting readCall resumes (1) and
		// the phase's `await readCall()` resumes one hop later (tool.ts:57, :121-122; scheduler.ts :1216-1228, :1269-1272).
		r.hops(readCallReturnHops, func() { r.readCallFrom(entry, then) })
	})
}

// readCallReturnHops is the hops from the entry read job's resolution to the tool phase resuming with the call.
const readCallReturnHops = 3

// executeReturnHops is the hops from the host's toolDone (after `await tool.execute()` and `await Promise.allSettled(admissions)`,
// which the host spends) to finalResult starting: `await progress.stop()` of an idle Progress, its `await this.#inFlight` and its return.
const executeReturnHops = 2

// finalResultReturnHops is the hop from finalResult returning the settled result to run resuming (tool.ts run).
const finalResultReturnHops = 1

func (r *toolRun) readCallFrom(entry *jv.Object, then func(call *jv.Object, ok bool)) {
	callID := str(r.input, "callId")
	if models, ok := arrayField(entry, "model"); ok && len(models) > 0 {
		if m, ok := models[0].(*jv.Object); ok && str(m, "role") == "assistant" {
			content, _ := arrayField(m, "content")
			for _, c := range content {
				if o, ok := c.(*jv.Object); ok && str(o, "type") == "toolCall" && str(o, "id") == callID {
					then(o, true)
					return
				}
			}
		}
	}
	r.fail(errors.New("Entry " + itoa(int64(idField(r.input, "assistant"))) + " has no tool call " + callID))
}

// findTool resolves the called tool among the phase agent's tools; then receives nil when the agent has no such tool.
func (r *toolRun) findTool(name string, then func(*Tool)) {
	r.agent(func(agent *Agent) {
		// A model-issued call resolves among the tools the model is offered, a nested call among those tools may call.
		list := agent.Tools
		if r.isNested() {
			list = agent.Callable
		}
		var found *Tool
		for i := range list {
			if nameOf(list[i].Declaration) == name {
				found = &list[i]
				break
			}
		}
		// resolveTool is an async function: the phase's `await resolveTool(...)` resumes one hop after it returns (tool.ts).
		r.hops(resolveToolReturnHops, func() { then(found) })
	})
}

// resolveToolReturnHops is the hop from resolveTool returning its tool to the phase resuming.
const resolveToolReturnHops = 1

// Ending is how a tool task ends; the result entry is appended either way. failed (execution threw or was interrupted)
// records cancellation intent for the conversations the call owns; a result with isError still completes.
type ending struct {
	status  string // "completed", "aborted" or "failed"
	message string
}

// Read-only: assigned once at initialization.
var completed = ending{status: "completed"}

func (r *toolRun) call() {
	h := r.g.Host
	r.readCall(func(call *jv.Object, _ bool) {
		name := str(call, "name")
		r.findTool(name, func(tool *Tool) {
			if tool == nil {
				r.settle(call, completed, func(*delta.Obj) *jv.Object { return HarnessError("tool_unavailable", "Tool "+name+" is not available") }, nil)
				return
			}
			args := objField(call, "arguments")
			if args == nil {
				args = jv.NewObject()
			}
			invalid := func(text string) {
				r.settle(call, completed, func(*delta.Obj) *jv.Object { return HarnessError("invalid_arguments", text) }, nil)
			}
			h.PrepareArguments(name, args, func(prepared any, errText string, err error) {
				if err != nil {
					r.fail(err)
					return
				}
				if errText != "" {
					invalid(errText)
					return
				}
				h.ValidateArguments(name, call, prepared, func(checked *jv.Object, errText string, err error) {
					if err != nil {
						r.fail(err)
						return
					}
					if errText != "" {
						invalid(errText)
						return
					}
					r.beforeTool(call, tool, checked, invalid)
				})
			})
		})
	})
}

func (r *toolRun) beforeTool(call *jv.Object, tool *Tool, args *jv.Object, invalid func(string)) {
	h := r.g.Host
	var block *string
	r.eachHookRaw("beforeTool", func() *jv.Object {
		withArgs := call.Shallow()
		withArgs.Set("arguments", args)
		return r.hookPayload(jobj("call", withArgs))
	}, func(result any, err error) (bool, error) {
		if err != nil {
			// A throw blocks the call with its message, unless the invocation was aborted (tool.ts beforeTool).
			if r.rt.Signal().Aborted() || errors.Is(err, session.ErrAborted) {
				return true, err
			}
			text := errorText(err)
			block = &text
			return true, nil
		}
		if o, ok := result.(*jv.Object); ok {
			if b, has := o.Get("block"); has && b != nil {
				text := jsString(b)
				block = &text
				return true, nil
			}
			if a := objField(o, "arguments"); a != nil {
				args = a
			}
		}
		return false, nil
	}, func(err error) {
		if err != nil {
			r.fail(err)
			return
		}
		if block != nil {
			blocked := *block
			r.settle(call, completed, func(*delta.Obj) *jv.Object { return HarnessError("blocked", "Tool call blocked: "+blocked) }, nil)
			return
		}
		h.ValidateArguments(str(call, "name"), call, args, func(final *jv.Object, errText string, err error) {
			if err != nil {
				r.fail(err)
				return
			}
			if errText != "" {
				invalid(errText)
				return
			}
			replay := tool.Replay
			if replay == "" {
				replay = "unsafe"
			}
			r.commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
				live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
				if err != nil {
					return nil, err
				}
				if slot := ToolSlotOf(live, r.rt.TaskID()); slot != nil {
					if err := slot.Set("status", "running"); err != nil {
						return nil, err
					}
					// A nested slot shows the arguments the call runs with, which repair, hooks, and coercion may have changed.
					// They are compared with the input, which the slot got at admission: reading the draft would track every leaf.
					if r.isNested() && IsNestedSlot(slot) && !jv.Equal(objField(objField(r.input, "call"), "arguments"), final) {
						if err := slot.Set("arguments", jv.Clone(final)); err != nil {
							return nil, err
						}
					}
				}
				intent := jobj("phase", phaseExecute, "arguments", final, "replay", replay)
				return jobj("status", "running", "checkpoint", intent), nil
			}, func() { r.runTool(call, tool, final) })
		})
	})
}

// execute is the recovery phase after intent: rerun only when the stored and the current policy both say safe.
func (r *toolRun) execute(cp *jv.Object) {
	args := objField(cp, "arguments")
	replay := str(cp, "replay")
	r.readCall(func(call *jv.Object, _ bool) {
		name := str(call, "name")
		r.findTool(name, func(tool *Tool) {
			if replay == "safe" && tool != nil && tool.Replay == "safe" {
				// The rerun reports from scratch; clear what the interrupted attempt published.
				r.commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
					live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
					if err != nil {
						return nil, err
					}
					if slot := ToolSlotOf(live, r.rt.TaskID()); slot != nil {
						ClearProgress(slot)
					}
					return nil, nil
				}, func() { r.runTool(call, tool, args) })
				return
			}
			message := "Tool " + name + " was interrupted and may have partially run"
			// failed records cancellation intent, so the call's owned conversations, left unsupervised, are aborted.
			r.settle(call, ending{status: "failed", message: message}, func(slot *delta.Obj) *jv.Object {
				return fromSlot(slot, "interrupted", message)
			}, nil)
		})
	})
}

// abort ends the call aborted with the durable partial output.
func (g *ToolTask) abort(rt *session.Runtime, task *jv.Object, done func(error)) {
	r := g.newRun(rt, task, done)
	r.readCall(func(call *jv.Object, _ bool) {
		code, message := "aborted", "Tool "+str(call, "name")+" was aborted"
		// Abandoned after a restart (abandonOnRestart): say whether the call may have run (tool.ts abort).
		if str(task, "abortReason") == "restart" {
			if str(checkpointOf(task), "phase") == phaseExecute {
				code, message = "interrupted", "Tool "+str(call, "name")+" was interrupted by a restart and may have partially run"
			} else {
				code, message = "abandoned", "Tool "+str(call, "name")+" was not started: its caller ended with a restart"
			}
		}
		r.settle(call, ending{status: "aborted"}, func(slot *delta.Obj) *jv.Object { return fromSlot(slot, code, message) }, nil)
	})
}

// settle commits the tool's terminal state: append its result entry, mark its slot done, and complete or end aborted with
// the entry ID. build receives the slot so interruption and abort can report the durable partial output. then, when given,
// runs after the commit instead of ending the phase.
func (r *toolRun) settle(call *jv.Object, end ending, build func(slot *delta.Obj) *jv.Object, durationMs *float64) {
	r.settleThen(call, end, build, durationMs, func() { r.done(nil) })
}

func (r *toolRun) settleThen(call *jv.Object, end ending, build func(slot *delta.Obj) *jv.Object, durationMs *float64, then func()) {
	// A crash in between leaves the call in execute: a safe rerun reattaches to the calls, an interruption ends here.
	r.nestedCallIds(func(nestedIds []ID) {
		if len(nestedIds) == 0 {
			// Nothing to abort: `await Promise.all([])` follows the memo's await with nothing between the three. The task's own memo
			// cannot change before then (no admission starts after the call ended, and those underway were awaited), so it is read
			// now and the settling commit starts after the ticks of all three awaits, on one chain.
			made := r.madeNested()
			r.hops(ownedTasksResumeTicks+memoResumeTicks+promiseAllResumeTicks, func() { r.settleCommit(call, end, build, durationMs, made, then) })
			return
		}
		// `await runtime.ownedTasks()` resumes one tick after the read job ends, `await runtime.memo(...)` three (#read over an
		// async arrow): tool.ts settle (tool_nested.go). The awaits before them are executeReturnHops and finalResultReturnHops.
		r.hops(ownedTasksResumeTicks, func() {
			// Read before the settling commit: no admission starts after the call ended, and those underway were awaited.
			made := r.madeNested()
			r.hops(memoResumeTicks, func() {
				// Mark them all before waiting for any: one may run until a sibling is cancelled.
				r.abortAll(nestedIds, func() { r.settleCommit(call, end, build, durationMs, made, then) })
			})
		})
	})
}

func (r *toolRun) settleCommit(call *jv.Object, end ending, build func(slot *delta.Obj) *jv.Object, durationMs *float64, made bool, then func()) {
	r.commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
		live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
		if err != nil {
			return nil, err
		}
		slot := ToolSlotOf(live, r.rt.TaskID())
		result := build(slot)
		var settled *jv.Object
		if r.isNested() {
			nested := nestedResultOf(r.rt.TaskID(), result, durationMs)
			if usage := objField(nested, "usage"); usage != nil {
				if err := recordUsageTx(tx, r.conv, UsageTools, str(call, "name"), usage); err != nil {
					return nil, err
				}
			}
			if err := r.storeNestedResult(tx, call, nested); err != nil {
				return nil, err
			}
			if slot != nil && IsNestedSlot(slot) {
				content, _ := arrayField(result, "output")
				if err := finishNestedSlot(slot, summaryOf(nested, content)); err != nil {
					return nil, err
				}
			}
			settled = jobj("kind", "nested")
		} else {
			entry, err := AppendToolResult(tx, r.conv, call, result, r.now(), durationMs)
			if err != nil {
				return nil, err
			}
			entryID := float64(idField(entry, "id"))
			if slot != nil && !IsNestedSlot(slot) {
				if err := FinishSlot(slot); err != nil {
					return nil, err
				}
				if err := slot.Set("entry", entryID); err != nil {
					return nil, err
				}
			}
			settled = jobj("kind", "model", "entryId", entryID)
			// Tools build control objects freely; keys set to undefined never reach strict JSON.
			if control, ok := result.Get("control"); ok && control != nil && end.status == "completed" {
				settled.Set("control", jv.Clone(control))
			}
		}
		// A call that made no nested calls has no slots below it; skipping the scan keeps many leaves linear.
		if made {
			if err := RemoveNestedSlots(live, r.rt.TaskID()); err != nil {
				return nil, err
			}
		}
		switch end.status {
		case "aborted":
			return jobj("status", "terminal", "outcome", jobj("status", "aborted", "result", settled)), nil
		case "failed":
			return jobj("status", "terminal", "outcome", jobj("status", "failed", "error", jobj("message", end.message), "result", settled)), nil
		}
		return jobj("status", "terminal", "outcome", jobj("status", "completed", "result", settled)), nil
	}, then)
}

// toolDiagnostic and truncated build the Harness's diagnostics.
func truncatedDiagnostic(droppedLines, droppedBytes int, retain string) *jv.Object {
	kept := ""
	switch retain {
	case "head":
		kept = " to its beginning"
	case "tail":
		kept = " to its end"
	}
	return diagnostic("warn", "truncated", "Output truncated"+kept+": "+itoa(int64(droppedLines))+" lines, "+itoa(int64(droppedBytes))+" bytes dropped")
}

// fromSlot is an error result from the slot's durable partial output, details and diagnostics.
func fromSlot(slot *delta.Obj, code, message string) *jv.Object {
	var diagnostics []any
	var output string
	var details any
	hasDetails := false
	droppedBytes, droppedLines := 0, 0
	if slot != nil {
		snap := slot.Snapshot()
		if list, ok := arrayField(snap, "diagnostics"); ok {
			diagnostics = append(diagnostics, list...)
		}
		droppedBytes = int(num(snap, "droppedBytes"))
		droppedLines = int(num(snap, "droppedLines"))
		output = str(snap, "output")
		details, hasDetails = snap.Get("details")
	}
	if droppedBytes > 0 {
		diagnostics = append(diagnostics, truncatedDiagnostic(droppedLines, droppedBytes, ""))
	}
	diagnostics = append(diagnostics, diagnostic("error", code, message))
	content := []any{}
	if output != "" {
		content = []any{jobj("type", "text", "text", output)}
	}
	result := jobj("output", content, "isError", true)
	if hasDetails {
		result.Set("details", details)
	}
	result.Set("diagnostics", diagnostics)
	return result
}

// reported is what a running tool reported through its api.
type reported struct {
	output       *OutputBuffer
	limits       OutputLimits
	diagnostics  []any
	details      any
	hasDetails   bool
	detailsEpoch int
}

func limitsOf(t *Tool) OutputLimits {
	l := OutputLimits{MaxBytes: defaultMaxBytes, MaxLines: defaultMaxLines, Retain: "head"}
	if t.Limits.MaxBytes != nil {
		l.MaxBytes = *t.Limits.MaxBytes
	}
	if t.Limits.MaxLines != nil {
		l.MaxLines = *t.Limits.MaxLines
	}
	if t.Limits.Retain != "" {
		l.Retain = t.Limits.Retain
	}
	return l
}

// runTool executes with the resolved implementation, then settles its result.
func (r *toolRun) runTool(call *jv.Object, tool *Tool, args *jv.Object) {
	h := r.g.Host
	limits := limitsOf(tool)
	rep := &reported{output: NewOutputBuffer(limits), limits: limits}
	prog := r.publishProgress(rep)
	ended := false
	io := &ToolIO{
		Output: func(chunk []byte, isString bool, skipped *Skip) error {
			if ended {
				return errors.New("Tool call " + str(call, "id") + " has settled")
			}
			var accepted bool
			if isString {
				accepted = rep.output.PushString(string(chunk), skipped)
			} else {
				accepted = rep.output.PushBytes(chunk, skipped)
			}
			if accepted {
				prog.mark()
			}
			return nil
		},
		Diagnostic: func(d *jv.Object) error {
			if ended {
				return errors.New("Tool call " + str(call, "id") + " has settled")
			}
			rep.diagnostics = append(rep.diagnostics, jv.Clone(d))
			prog.mark()
			return nil
		},
		Details: func(value any, then func(error)) {
			if ended {
				then(errors.New("Tool call " + str(call, "id") + " has settled"))
				return
			}
			if r.rt.Signal().Aborted() {
				then(session.ErrAborted)
				return
			}
			rep.details, rep.hasDetails = jv.Clone(value), true
			rep.detailsEpoch++
			prog.markAndWait(then)
		},
	}
	run := &ToolRun{Task: r.rt.TaskID(), Conversation: r.conv, CallID: str(call, "id"), Name: str(call, "name"), Arguments: args}
	if limits.Retain == "tail" {
		run.OutputWindow = jobj("maxBytes", float64(limits.MaxBytes), "maxLines", float64(limits.MaxLines),
			"minIntervalMs", h.Settings().OutputIntervalMs, "bytesPerSecond", float64(ProgressBytesPerSecond))
	}
	startedAt := h.Now()
	onDone := func(result *jv.Object, err error) {
		// Execution time of this attempt; a rerun after recovery measures only itself.
		duration := math.Floor(h.Now() - startedAt + 0.5)
		end := completed
		if err != nil {
			var fault *ResultFault
			if errors.As(err, &fault) || r.rt.Signal().Aborted() {
				ended = true
				prog.stop(func(waiters []func(error)) {
					for _, w := range waiters {
						w(err)
					}
					r.fail(err)
				})
				return
			}
			result = jobj("isError", true, "diagnostics", []any{diagnostic("error", "tool_error", errorText(err))})
			// A throw ends the task failed, which cancels what the call owned; it no longer supervises it. The error text is
			// already in the result entry.
			end = ending{status: "failed", message: "Tool " + str(call, "name") + " threw"}
		}
		ended = true
		rep.output.End()
		// Details still waiting for a progress commit settle with the terminal commit, the final flush. The host reports after
		// `await tool.execute()` and `await Promise.allSettled(admissions)`; `await progress.stop()` takes executeReturnHops (tool.ts run).
		stop := func(before int) {
			prog.stop(func(pending []func(error)) {
				r.finalResult(call, tool, result, rep, before, func(settled *jv.Object, err error, lead int) {
					// finalResult is an async function: `await finalResult(...)` resumes one tick after it returns or throws. lead
					// is the ticks that passed before it settled and that its caller has not spent.
					r.hops(lead+finalResultReturnHops, func() {
						if err != nil {
							for _, w := range pending {
								w(err)
							}
							r.fail(err)
							return
						}
						r.settleThen(call, end, func(*delta.Obj) *jv.Object { return settled }, &duration, func() {
							for _, w := range pending {
								w(nil)
							}
							r.done(nil)
						})
					})
				})
			})
		}
		if prog.idle() && r.finalResultPure(tool, result) {
			// Nothing waits for a progress commit and afterTool has no handler: the stop, the walk and the settled result run no host
			// code and no job, and nothing they read changes in the two ticks of `await progress.stop()`, so they run now and
			// those ticks are spent with the rest, on one chain.
			stop(executeReturnHops)
			return
		}
		r.hops(executeReturnHops, func() { stop(0) })
	}
	h.RunTool(run, io, r.rt.Signal(), onDone)
}

// publishProgress is the throttled commits of what the tool reported into its pi.live.tools slot, each writing only what
// changed since the last one.
func (r *toolRun) publishProgress(rep *reported) *progress {
	h := r.g.Host
	writtenText, writtenDiagnostics, writtenEpoch := "", 0, 0
	// A nested call made with progress: false commits only its status; its output stays in the result.
	enabled := true
	if off, ok := r.input.Get("progress"); r.isNested() && ok && off == false {
		enabled = false
	}
	write := func(then func(bytes int, err error)) {
		if !enabled {
			then(0, nil)
			return
		}
		// Capture everything synchronously: the tool keeps reporting while the commit is in flight.
		snapshot := rep.output.Snapshot()
		details, hasDetails, epoch := rep.details, rep.hasDetails, rep.detailsEpoch
		count := len(rep.diagnostics)
		added := append([]any{}, rep.diagnostics[writtenDiagnostics:count]...)
		detailsChanged := epoch != writtenEpoch
		// What the commit writes, as Chord diffs the string: an append, a trim plus an append of what follows the shared
		// part, or the whole window when its bounded overlap search finds nothing.
		bytes := 0
		if snapshot.Text != writtenText {
			var shared int
			if jv.HasPrefix(snapshot.Text, writtenText) {
				shared = jv.UTF16Len(writtenText)
			} else {
				shared = delta.Overlap(writtenText, snapshot.Text, 65536)
			}
			bytes += len(jv.SliceFrom(snapshot.Text, shared))
		}
		if detailsChanged {
			bytes += len(jv.Stringify(details)) // an unset value is JSON null
		}
		if len(added) > 0 {
			bytes += len(jv.Stringify(added))
		}
		r.rt.Commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
			live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
			if err != nil {
				return nil, err
			}
			slot := ToolSlotOf(live, r.rt.TaskID())
			if slot == nil {
				return nil, nil
			}
			// Assign output as one string field: Chord then diffs it into an append, or a trim plus an append for a sliding
			// tail; replacing the slot object would record the whole window on every commit.
			if cur, _ := slot.Get("output").(string); cur != snapshot.Text {
				if err := slot.Set("output", snapshot.Text); err != nil {
					return nil, err
				}
			}
			if snapshot.DroppedBytes > 0 {
				if err := slot.Set("droppedBytes", float64(snapshot.DroppedBytes)); err != nil {
					return nil, err
				}
			}
			if snapshot.DroppedLines > 0 {
				if err := slot.Set("droppedLines", float64(snapshot.DroppedLines)); err != nil {
					return nil, err
				}
			}
			// Diff details leaf by leaf and append new diagnostics, so each commit writes only what changed.
			if detailsChanged && hasDetails {
				if err := assignObj(slot, "details", details); err != nil {
					return nil, err
				}
			}
			if len(added) > 0 {
				if !slot.Has("diagnostics") {
					if err := slot.Set("diagnostics", []any{}); err != nil {
						return nil, err
					}
				}
				if _, err := slot.Arr("diagnostics").Push(added...); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}, func(err error) {
			if err == nil {
				writtenText, writtenDiagnostics, writtenEpoch = snapshot.Text, count, epoch
			}
			then(bytes, err)
		})
	}
	onError := func(err error) {
		// Rejections after an abort mark or close are expected; the committed state stays consistent.
		if !r.rt.Signal().Aborted() {
			_ = r.rt.Report(err)
		}
	}
	return newProgress(h, write, onError, h.Settings().OutputIntervalMs)
}

// finalResult is the settled result: the tool's result with the retained output and last details as fallbacks, its
// diagnostics after those reported through the api, afterTool applied, and explicit text bounded, with the Harness's
// truncation diagnostic last.
//
// before is the ticks of the caller's awaits that have not passed yet: only with finalResultPure, which runs it at once, and then
// they are part of lead, the ticks the caller spends before `await finalResult()` resumes.
func (r *toolRun) finalResult(call *jv.Object, tool *Tool, result *jv.Object, rep *reported, before int, then func(settled *jv.Object, err error, lead int)) {
	h := r.g.Host
	var harness []any
	var retained *BoundedOutput
	var content []any
	if resultContent, ok := arrayField(result, "output"); ok {
		content = resultContent
	} else {
		snap := rep.output.Snapshot()
		retained = &snap
		content = []any{}
		if snap.Text != "" {
			content = []any{jobj("type", "text", "text", snap.Text)}
		}
	}
	final := result.Shallow()
	final.Set("output", content)
	if _, ok := result.Get("details"); !ok && rep.hasDetails {
		final.Set("details", rep.details)
	}
	diagnostics := append([]any{}, rep.diagnostics...)
	if list, ok := arrayField(result, "diagnostics"); ok {
		diagnostics = append(diagnostics, list...)
	}
	final.Set("diagnostics", diagnostics)
	// settle is what follows afterTool: lead is the ticks of hooks.each that already passed when it runs (see below).
	settle := func(err error, lead int) {
		if err != nil {
			then(nil, err, lead)
			return
		}
		finalContent, _ := arrayField(final, "output")
		// The retained output's truncation applies only while afterTool kept that content.
		if retained != nil && retained.DroppedBytes > 0 && jv.Equal(finalContent, content) {
			harness = append(harness, truncatedDiagnostic(retained.DroppedLines, retained.DroppedBytes, rep.limits.Retain))
		}
		bounded, droppedBytes, droppedLines := boundContent(finalContent, rep.limits)
		if droppedBytes > 0 {
			harness = append(harness, truncatedDiagnostic(droppedLines, droppedBytes, rep.limits.Retain))
		}
		out := final.Shallow()
		out.Set("output", bounded)
		finish := func(broken string) {
			if broken != "" {
				out.Delete("structuredOutput")
				if r.isNested() {
					// A nested call whose structuredOutput breaks the contract gets an error result without it.
					out.Set("isError", true)
					harness = append(harness, diagnostic("error", "invalid_structured_output", broken))
				} else {
					// The model never sees structured output, so its call stands; the host learns of the broken tool.
					// Only names the extension: a failed resolution leaves the report without the name.
					_ = r.rt.Report(errors.New(broken), session.ReportFrom{Extension: tool.Extension})
				}
			}
			// Programs get the tool's own value: without a schema, its output. The model reads only content, so model-issued calls
			// skip both.
			if r.isNested() && !tool.StructuredOutput {
				out.Set("structuredOutput", outputValue(bounded))
			}
			list, _ := arrayField(final, "diagnostics")
			// A schema tool's error result without structured output has its text only in the output, which programs never get:
			// it becomes an error diagnostic, as a thrown error's message does, unless one says what went wrong already (Pi 1.2.0
			// tool.ts finalResult :534-546).
			if isError, _ := out.Get("isError"); r.isNested() && tool.StructuredOutput && isError == true {
				if _, has := out.Get("structuredOutput"); !has && !hasErrorDiagnostic(list) && !hasErrorDiagnostic(harness) {
					var texts []string
					for _, item := range bounded {
						if o, ok := item.(*jv.Object); ok && str(o, "type") == "text" {
							texts = append(texts, str(o, "text"))
						}
					}
					if text := strings.Join(texts, "\n"); text != "" {
						harness = append(harness, diagnostic("error", "tool_error", text))
					}
				}
			}
			out.Set("diagnostics", append(append([]any{}, list...), harness...))
			then(out, nil, lead)
		}
		// The host holds the schema and judges the settled result, after afterTool (structuredOutputError); a result with no
		// structured output from a tool with no schema keeps the contract without asking.
		if _, has := out.Get("structuredOutput"); !has && !tool.StructuredOutput {
			finish("")
			return
		}
		probe := jv.NewObject()
		if value, has := out.Get("structuredOutput"); has {
			probe.Set("structuredOutput", value)
		}
		if isError, has := out.Get("isError"); has {
			probe.Set("isError", isError)
		}
		h.CheckStructuredOutput(str(call, "name"), probe, func(text string, err error) {
			if err != nil {
				then(nil, err, lead)
				return
			}
			finish(text)
		})
	}
	if r.finalResultPure(tool, result) {
		// No handler: hooks.each takes its two ticks (eachHookRaw) and what follows computes the settled result from the values at
		// hand without a host call, so it runs now and the caller spends those ticks with its own: one chain, not three.
		settle(nil, before+eachHookNoHandlerHops)
		return
	}
	r.eachHook("afterTool", func() *jv.Object { return r.hookPayload(jobj("call", call, "result", final)) }, func(res any) bool {
		if o, ok := res.(*jv.Object); ok {
			final = o
		}
		return false
	}, func(err error) { settle(err, 0) })
}

// finalResultPure reports whether finalResult settles the result without a host call: afterTool has no handler (the walk is
// eachHookRaw's fixed ticks) and no structuredOutput is judged by the host.
func (r *toolRun) finalResultPure(tool *Tool, result *jv.Object) bool {
	_, hasStructured := result.Get("structuredOutput")
	return !hasStructured && !tool.StructuredOutput && r.agentSeen && r.g.Host.Hooks != nil && r.g.Host.Hooks("afterTool", r.conv) == 0
}

// eachHookNoHandlerHops is the ticks hooks.each takes with no handler: `await agent()` of a settled resolution and the caller's
// await of the async function (eachHookRaw).
const eachHookNoHandlerHops = 2

// hasErrorDiagnostic reports whether one of diagnostics has severity error.
func hasErrorDiagnostic(diagnostics []any) bool {
	for _, item := range diagnostics {
		if o, ok := item.(*jv.Object); ok && str(o, "severity") == "error" {
			return true
		}
	}
	return false
}

// boundContent bounds the text of result content. When the joined text exceeds the limits, the text items are replaced by
// one bounded item at the position of the first (head) or last (tail) text item; other content is kept.
func boundContent(content []any, limits OutputLimits) ([]any, int, int) {
	var texts []*jv.Object
	var joined strings.Builder
	for _, item := range content {
		if o, ok := item.(*jv.Object); ok && str(o, "type") == "text" {
			texts = append(texts, o)
			joined.WriteString(str(o, "text"))
		}
	}
	bounded := BoundOutput(joined.String(), limits)
	if bounded.DroppedBytes == 0 {
		return content, 0, 0
	}
	keep := texts[0]
	if limits.Retain != "head" {
		keep = texts[len(texts)-1]
	}
	out := []any{}
	for _, item := range content {
		o, ok := item.(*jv.Object)
		if !ok || str(o, "type") != "text" {
			out = append(out, item)
		} else if o == keep {
			c := o.Shallow()
			c.Set("text", bounded.Text)
			out = append(out, c)
		}
	}
	return out, bounded.DroppedBytes, bounded.DroppedLines
}

// errorText is tool.ts errorText: the message of a thrown Error. A host error that crossed the boundary as {name, message}
// reports its message (Text); any other error reports its own text.
func errorText(err error) string {
	if t, ok := err.(interface{ Text() string }); ok {
		return t.Text()
	}
	return err.Error()
}
