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
	r := gen.newRun(rt, done)
	r.task = task
	return &toolRun{run: r, input: objField(task, "input")}
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
			r.rt.ToolCall(str(call, "name"), str(call, "id"))
			then(call, true)
			return
		}
		r.fail(errors.New("Nested call " + str(r.input, "key") + " has no call"))
		return
	}
	r.entry(idField(r.input, "assistant"), func(entry *jv.Object, kept bool) {
		// `#read` returns the arrow's promise from an async function (adoption, 2), runtime.entry's awaiting readCall resumes (1) and
		// the phase's `await readCall()` resumes one hop later (tool.ts:57, :121-122; scheduler.ts :1216-1228, :1269-1272). An entry of the
		// kept range is already at runtime.entry's caller (keptEntryHops covers the adoption and that resumption): only the phase's resumes.
		hops := readCallReturnHops
		if kept {
			hops = readCallKeptReturnHops
		}
		r.hops(hops, func() { r.readCallFrom(entry, then) })
	})
}

// readCallReturnHops is the hops from the entry read job's resolution to the tool phase resuming with the call.
const readCallReturnHops = 3

// readCallKeptReturnHops is the hop from readCall resuming with an entry of the kept range to the tool phase resuming with the call.
const readCallKeptReturnHops = 1

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
					r.rt.ToolCall(str(o, "name"), callID)
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
	// reason is the detail.reason of a failure that crashes caused (restart_limit); empty otherwise.
	reason string
}

// Read-only: assigned once at initialization.
var completed = ending{status: "completed"}

func (r *toolRun) call() {
	c := &callRun{r: r}
	r.readCall(c.read)
}

// callRun is the call phase of one tool call: resolving the tool, preparing and validating the arguments, beforeTool, and the intent.
type callRun struct {
	r        *toolRun
	call     *jv.Object
	tool     *Tool
	name     string
	args     *jv.Object
	final    *jv.Object
	blocked  string
	hasBlock bool
	replay   string
}

func (c *callRun) read(call *jv.Object, _ bool) {
	r := c.r
	c.call = call
	if r.cutOffTooOften(r.task) {
		r.endCutOff(call)
		return
	}
	c.name = str(call, "name")
	if r.isUnoffered() {
		// Settles `tool_unavailable` without resolving the name.
		r.reject(call, nil, HarnessError("tool_unavailable", "Tool "+c.name+" is not available"))
		return
	}
	r.findTool(c.name, c.found)
}

func (c *callRun) found(tool *Tool) {
	r := c.r
	if tool == nil {
		r.reject(c.call, nil, HarnessError("tool_unavailable", "Tool "+c.name+" is not available"))
		return
	}
	c.tool = tool
	c.args = objField(c.call, "arguments")
	if c.args == nil {
		c.args = jv.NewObject()
	}
	r.g.Host.PrepareArguments(c.name, c.args, c.prepared)
}

func (c *callRun) invalid(text string) {
	c.r.reject(c.call, c.tool, HarnessError("invalid_arguments", text))
}

func (c *callRun) prepared(prepared any, errText string, err error) {
	if err != nil {
		c.r.fail(err)
		return
	}
	if errText != "" {
		c.invalid(errText)
		return
	}
	c.r.g.Host.ValidateArguments(c.name, c.call, prepared, c.checked)
}

func (c *callRun) checked(checked *jv.Object, errText string, err error) {
	if err != nil {
		c.r.fail(err)
		return
	}
	if errText != "" {
		c.invalid(errText)
		return
	}
	c.args = checked
	c.r.eachHookRaw("beforeTool", c.payload, c.hookResult, c.hooked)
}

func (c *callRun) payload() *jv.Object {
	withArgs := c.call.Shallow()
	withArgs.Set("arguments", c.args)
	return c.r.hookPayload(jobj("call", withArgs))
}

func (c *callRun) hookResult(result any, err error) (bool, error) {
	r := c.r
	if err != nil {
		// A throw blocks the call with its message, unless the invocation was aborted (tool.ts beforeTool).
		if r.rt.Signal().Aborted() || errors.Is(err, session.ErrAborted) {
			return true, err
		}
		c.blocked, c.hasBlock = errorText(err), true
		return true, nil
	}
	if o, ok := result.(*jv.Object); ok {
		if b, has := o.Get("block"); has && b != nil {
			c.blocked, c.hasBlock = jsString(b), true
			return true, nil
		}
		if a := objField(o, "arguments"); a != nil {
			c.args = a
		}
	}
	return false, nil
}

func (c *callRun) hooked(err error) {
	r := c.r
	if err != nil {
		r.fail(err)
		return
	}
	if c.hasBlock {
		r.reject(c.call, c.tool, HarnessError("blocked", "Tool call blocked: "+c.blocked))
		return
	}
	r.g.Host.ValidateArguments(c.name, c.call, c.args, c.validated)
}

func (c *callRun) validated(final *jv.Object, errText string, err error) {
	r := c.r
	if err != nil {
		r.fail(err)
		return
	}
	if errText != "" {
		c.invalid(errText)
		return
	}
	c.final = final
	c.replay = c.tool.Replay
	if c.replay == "" {
		c.replay = "unsafe"
	}
	// The slot's status and arguments are presentation, stored like progress; the intent is durable before the tool runs.
	// Parallel calls of one round then share no immediate write to pi.live.
	r.commitProgress(c.showRunning, c.stored)
}

func (c *callRun) showRunning(tx *session.Tx) error {
	r := c.r
	live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
	if err != nil {
		return err
	}
	if slot := ToolSlotOf(live, r.rt.TaskID()); slot != nil {
		if err := slot.Set("status", "running"); err != nil {
			return err
		}
		// A nested slot shows the arguments the call runs with, which repair, hooks, and coercion may have changed.
		// Assigned whole: an equal value records no change, and a rerun corrects what an earlier attempt showed.
		if IsNestedSlot(slot) {
			if err := slot.Set("arguments", jv.Clone(c.final)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *callRun) stored() { c.r.commit(c.intent, c.run) }

func (c *callRun) intent(*session.Tx, *jv.Object) (*jv.Object, error) {
	intent := jobj("phase", phaseExecute, "arguments", c.final, "replay", c.replay)
	return jobj("status", "running", "checkpoint", intent), nil
}

func (c *callRun) run() { c.r.runTool(c.call, c.tool, c.final) }

// execute is the recovery phase after intent: rerun only when the stored and the current policy both say safe.
func (r *toolRun) execute(cp *jv.Object) {
	args := objField(cp, "arguments")
	replay := str(cp, "replay")
	r.readCall(func(call *jv.Object, _ bool) {
		// Also before an interruption result: its `afterTool` hooks may be what cut the task off.
		if r.cutOffTooOften(r.task) {
			r.endCutOff(call)
			return
		}
		name := str(call, "name")
		r.findTool(name, func(tool *Tool) {
			if replay == "safe" && tool != nil && tool.Replay == "safe" {
				// The rerun reports from scratch: clear what the interrupted attempt published, and show the slot running with the
				// arguments it runs with again, as the deferred commit that showed them may have been lost with the crash.
				r.commit(func(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
					live, err := tx.Doc(LiveDef, session.DocArgs{Owner: r.conv})
					if err != nil {
						return nil, err
					}
					if slot := ToolSlotOf(live, r.rt.TaskID()); slot != nil {
						ClearProgress(slot)
						if err := slot.Set("status", "running"); err != nil {
							return nil, err
						}
						if IsNestedSlot(slot) {
							if err := slot.Set("arguments", jv.Clone(args)); err != nil {
								return nil, err
							}
						}
					}
					return nil, nil
				}, func() { r.runTool(call, tool, args) })
				return
			}
			message := "Tool " + name + " was interrupted and may have partially run"
			// failed records cancellation intent, so the call's owned conversations, left unsupervised, are aborted. Not cut off too often
			// (checked above), so the hooks run.
			r.settleFromSlot(call, true, tool, ending{status: "failed", message: message}, "interrupted", message)
		})
	})
}

// endCutOff ends a call cut off settings.restartLimit times in a row (spec section 5.4) with an error result, built from the slot
// like an interrupted call's, instead of running it again. failed, like an interruption, so its owned work is aborted.
func (r *toolRun) endCutOff(call *jv.Object) {
	message := "Tool " + str(call, "name") + " was interrupted " + cutOffCount(r.task) + "; it is not run again"
	end := ending{status: "failed", message: message, reason: "restart_limit"}
	// Past the limit the hooks may be what keeps cutting the task off (`salvageable`).
	if r.salvageable(r.task) {
		r.toolForLimits(str(call, "name"), func(tool *Tool) { r.settleFromSlot(call, true, tool, end, "restart_limit", message) })
		return
	}
	r.settleFromSlot(call, false, nil, end, "restart_limit", message)
}

// abort ends the call aborted with the durable partial output.
func (g *ToolTask) abort(rt *session.Runtime, task *jv.Object, done func(error)) {
	r := g.newRun(rt, task, done)
	r.readCall(func(call *jv.Object, _ bool) {
		// Abandoned after a restart (abandonOnRestart): say whether the call may have run (tool.ts abort). An unoffered call could not run in
		// any case, and its result says so.
		code, message := "aborted", "Tool "+str(call, "name")+" was aborted"
		switch {
		case r.isUnoffered():
			code, message = "tool_unavailable", "Tool "+str(call, "name")+" is not available"
		case str(task, "abortReason") == "restart":
			if str(checkpointOf(task), "phase") == phaseExecute {
				code, message = "interrupted", "Tool "+str(call, "name")+" was interrupted by a restart and may have partially run"
			} else {
				code, message = "abandoned", "Tool "+str(call, "name")+" was not started: its caller ended with a restart"
			}
		}
		// The scheduler runs an abort handler only while its restarts are below the limit, and faults the task without a result when the
		// last attempt is cut off. That attempt skips the hooks, which may be what cuts it off.
		if num(task, "restarts") < g.Host.Settings().RestartLimit-1 {
			r.toolForLimits(str(call, "name"), func(tool *Tool) {
				r.settleFromSlot(call, true, tool, ending{status: "aborted"}, code, message)
			})
			return
		}
		r.settleFromSlot(call, false, nil, ending{status: "aborted"}, code, message)
	})
}

// settle commits the tool's terminal state: append its result entry, mark its slot done, and complete or end aborted with
// the entry ID. build receives the slot so interruption and abort can report the durable partial output. then, when given,
// runs after the commit instead of ending the phase.
func (r *toolRun) settle(call *jv.Object, end ending, build func(slot *delta.Obj) *jv.Object, durationMs *float64) {
	r.settleThen(call, end, build, durationMs, r.finished)
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
	r.commit(func(tx *session.Tx, current *jv.Object) (*jv.Object, error) {
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
			failure := jobj("message", end.message)
			if end.reason != "" {
				failure.Set("detail", jobj("reason", end.reason))
			}
			return jobj("status", "terminal", "outcome", jobj("status", "failed", "error", failure, "result", settled)), nil
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

// fromSlot is an error result from the slot's durable partial output, details and diagnostics (tool.ts fromSlot): snap is the slot's
// value in pi.live, nil when it lists none. truncation describes what the retained output lost, kept apart: the caller adds it after
// the hooks, or to the diagnostics as built.
func fromSlot(snap *jv.Object, code, message string) (result, truncation *jv.Object) {
	var diagnostics []any
	var output string
	var details any
	hasDetails := false
	if snap != nil {
		if list, ok := arrayField(snap, "diagnostics"); ok {
			diagnostics = append(diagnostics, list...)
		}
		if droppedBytes := int(num(snap, "droppedBytes")); droppedBytes > 0 {
			truncation = truncatedDiagnostic(int(num(snap, "droppedLines")), droppedBytes, "")
		}
		output = str(snap, "output")
		details, hasDetails = snap.Get("details")
	}
	diagnostics = append(diagnostics, diagnostic("error", code, message))
	content := []any{}
	if output != "" {
		content = []any{jobj("type", "text", "text", output)}
	}
	result = jobj("output", content, "isError", true)
	if hasDetails {
		result.Set("details", details)
	}
	result.Set("diagnostics", diagnostics)
	return result, truncation
}

// slotSnapshot is the slot of tool task taskID in a pi.live value, model-issued or nested; nil when pi.live no longer lists it.
func slotSnapshot(live *jv.Object, taskID ID) *jv.Object {
	for _, key := range [...]string{"tools", "nestedTools"} {
		list, _ := arrayField(live, key)
		for _, item := range list {
			if slot, ok := item.(*jv.Object); ok && slot.Has("taskId") && ID(num(slot, "taskId")) == taskID {
				return slot
			}
		}
	}
	return nil
}

// isUnoffered is a model-issued call to a tool its request did not offer (spec section 8.3).
func (r *toolRun) isUnoffered() bool {
	return !r.isNested() && r.input != nil && r.input.Has("unoffered") && mustTrue(r.input, "unoffered")
}

// toolForLimits is the tool whose output limits bound a hooks' result: the called tool, or none when it, or the agent, fails to resolve
// (tool.ts `resolveTool(...).catch(() => undefined)`), and for an unoffered call, which never resolves its name. It is an async function
// its caller awaits: after the tool's resolution the catch reaction (1), the adoption of the returned promise (2) and the caller's
// await (1) follow, and an unoffered call returns at once, one hop for the await.
func (r *toolRun) toolForLimits(name string, then func(*Tool)) {
	if r.isUnoffered() {
		r.hops(1, func() { then(nil) })
		return
	}
	finish := func(tool *Tool) { r.hops(resolveToolReturnHops+toolForLimitsCatchHops, func() { then(tool) }) }
	pick := func(agent *Agent) {
		list := agent.Tools
		if r.isNested() {
			list = agent.Callable
		}
		for i := range list {
			if nameOf(list[i].Declaration) == name {
				finish(&list[i])
				return
			}
		}
		finish(nil)
	}
	if r.agentSeen {
		r.agent(pick)
		return
	}
	r.agentAfterOr(agentHops, pick, func(error) { finish(nil) })
}

// toolForLimitsCatchHops is the hops toolForLimits adds to resolveTool's own: the `.catch` reaction, the adoption of its promise by the
// async function that returns it, and the caller's await.
const toolForLimitsCatchHops = 3

// reject settles a result the Harness decided in the `call` phase, without running the tool: the tool did not resolve, the arguments are
// invalid, or `beforeTool` blocked the call. `afterTool` applies as to an executed call's result.
func (r *toolRun) reject(call *jv.Object, tool *Tool, result *jv.Object) {
	r.afterHarnessResult(call, tool, result, nil, func(settled *jv.Object, err error) {
		if err != nil {
			r.fail(err)
			return
		}
		r.settle(call, completed, func(*delta.Obj) *jv.Object { return settled }, nil)
	})
}

// settleFromSlot settles an error result built from the call's slot (fromSlot): an interruption, the restart limit, or an abort. The
// tool is not running, so the slot read before the commit is still current when it commits. With hooks, the result passes `afterTool` and
// is bounded with the limits of tool (the defaults when it is nil); without, it is settled as built, when the hooks may be what cuts the
// task off.
func (r *toolRun) settleFromSlot(call *jv.Object, hooks bool, tool *Tool, end ending, code, message string) {
	r.snapshot(LiveDef, func(live *jv.Object) {
		result, truncation := fromSlot(slotSnapshot(live, r.rt.TaskID()), code, message)
		if hooks {
			r.afterHarnessResult(call, tool, result, truncation, func(settled *jv.Object, err error) {
				if err != nil {
					r.fail(err)
					return
				}
				r.settle(call, end, func(*delta.Obj) *jv.Object { return settled }, nil)
			})
			return
		}
		settled := result
		if truncation != nil {
			list, _ := arrayField(result, "diagnostics")
			settled = result.Shallow()
			settled.Set("diagnostics", append(append([]any{}, list...), truncation))
		}
		r.settle(call, end, func(*delta.Obj) *jv.Object { return settled }, nil)
	})
}

// afterHarnessResult is a result the Harness wrote itself, without executing the tool, after the `afterTool` chain, normalized like an
// executed call's: the output bounded with the tool's limits (the defaults when it did not resolve), a truncation diagnostic when that
// dropped something, and the hooks' structuredOutput dropped, since the tool never produced one. A nested call that the hooks turned into
// a success gets the bounded output as its value, as a schema-less tool's success does, unless the tool declares a schema. usage is kept
// and recorded; control applies only when the task completes. truncation describes what the result's own output lost before (a slot's
// retained output); it is kept only while the hooks keep that output. It is an async function its caller awaits: the caller resumes a hop
// after it returns or throws (tool.ts afterHarnessResult). Measured on Pi e7a0df27 with the cut-off tool task of cutoff-ticks.test.ts: 3 hops
// from settleFromSlot's await to settle with no handler (hooks.each's two and this).
func (r *toolRun) afterHarnessResult(call *jv.Object, tool *Tool, result, truncation *jv.Object, resume func(*jv.Object, error)) {
	then := func(settled *jv.Object, err error) { r.hops(1, func() { resume(settled, err) }) }
	final := result
	r.eachHook("afterTool", func() *jv.Object { return r.hookPayload(jobj("call", call, "result", final)) }, func(res any) bool {
		if o, ok := res.(*jv.Object); ok {
			final = o
		}
		return false
	}, func(err error) {
		if err != nil {
			then(nil, err)
			return
		}
		var harness []any
		finalContent, _ := arrayField(final, "output")
		resultContent, _ := arrayField(result, "output")
		if truncation != nil && jv.Equal(finalContent, resultContent) {
			harness = append(harness, truncation)
		}
		limits := OutputLimits{MaxBytes: defaultMaxBytes, MaxLines: defaultMaxLines, Retain: "head"}
		if tool != nil {
			limits = limitsOf(tool)
		}
		bounded, droppedBytes, droppedLines := boundContent(finalContent, limits)
		if droppedBytes > 0 {
			harness = append(harness, truncatedDiagnostic(droppedLines, droppedBytes, limits.Retain))
		}
		out := final.Shallow()
		out.Delete("structuredOutput")
		if isError, _ := out.Get("isError"); r.isNested() && isError != true && (tool == nil || !tool.StructuredOutput) {
			out.Set("structuredOutput", outputValue(bounded))
		}
		out.Set("output", bounded)
		list, _ := arrayField(final, "diagnostics")
		out.Set("diagnostics", append(append([]any{}, list...), harness...))
		then(out, nil)
	})
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

// toolExec is one execution of a tool: what it reported through its api, the progress that publishes it, and the state the steps
// that settle its result share. The api and the steps are its methods, so an execution holds one frame instead of a box for each
// variable its callbacks read.
type toolExec struct {
	r         *toolRun
	call      *jv.Object
	tool      *Tool
	rep       *reported
	prog      *progress
	startedAt float64
	// ended is set once the tool returned: later reports are refused.
	ended bool
	// The result being settled, how it ends, the time the attempt took, the waiters of the final progress commit and the error the
	// steps hand on.
	result   *jv.Object
	end      ending
	duration float64
	pending  []func(error)
	before   int
	settled  *jv.Object
	err      error
}

// runTool executes with the resolved implementation, then settles its result.
func (r *toolRun) runTool(call *jv.Object, tool *Tool, args *jv.Object) {
	h := r.g.Host
	limits := limitsOf(tool)
	x := &toolExec{r: r, call: call, tool: tool, rep: &reported{output: NewOutputBuffer(limits), limits: limits}}
	x.prog = r.publishProgress(x.rep)
	io := &ToolIO{Output: x.output, Diagnostic: x.diagnostic, Details: x.details}
	run := &ToolRun{Task: r.rt.TaskID(), Conversation: r.conv, CallID: str(call, "id"), Name: str(call, "name"), Arguments: args}
	if limits.Retain == "tail" {
		run.OutputWindow = jobj("maxBytes", float64(limits.MaxBytes), "maxLines", float64(limits.MaxLines),
			"minIntervalMs", h.Settings().OutputIntervalMs, "bytesPerSecond", float64(ProgressBytesPerSecond))
	}
	x.startedAt = h.Now()
	h.RunTool(run, io, r.rt.Signal(), x.done)
}

func (x *toolExec) settledError() error {
	return errors.New("Tool call " + str(x.call, "id") + " has settled")
}

func (x *toolExec) output(chunk []byte, isString bool, skipped *Skip) error {
	if x.ended {
		return x.settledError()
	}
	var accepted bool
	if isString {
		accepted = x.rep.output.PushString(string(chunk), skipped)
	} else {
		accepted = x.rep.output.PushBytes(chunk, skipped)
	}
	if accepted {
		x.prog.mark()
	}
	return nil
}

func (x *toolExec) diagnostic(d *jv.Object) error {
	if x.ended {
		return x.settledError()
	}
	x.rep.diagnostics = append(x.rep.diagnostics, jv.Clone(d))
	x.prog.mark()
	return nil
}

func (x *toolExec) details(value any, then func(error)) {
	if x.ended {
		then(x.settledError())
		return
	}
	if x.r.rt.Signal().Aborted() {
		then(session.ErrAborted)
		return
	}
	x.rep.details, x.rep.hasDetails = jv.Clone(value), true
	x.rep.detailsEpoch++
	x.prog.markAndWait(then)
}

// done is the tool's return: its result, or the error it threw.
func (x *toolExec) done(result *jv.Object, err error) {
	r := x.r
	// Execution time of this attempt; a rerun after recovery measures only itself.
	x.duration = math.Floor(r.g.Host.Now() - x.startedAt + 0.5)
	x.result, x.end = result, completed
	if err != nil {
		var fault *ResultFault
		if errors.As(err, &fault) || r.rt.Signal().Aborted() {
			x.ended, x.err = true, err
			x.prog.stop(x.abandoned)
			return
		}
		x.result = jobj("isError", true, "diagnostics", []any{diagnostic("error", "tool_error", errorText(err))})
		// A throw ends the task failed, which cancels what the call owned; it no longer supervises it. The error text is
		// already in the result entry.
		x.end = ending{status: "failed", message: "Tool " + str(x.call, "name") + " threw"}
	}
	x.ended = true
	x.rep.output.End()
	// Details still waiting for a progress commit settle with the terminal commit, the final flush. The host reports after
	// `await tool.execute()` and `await Promise.allSettled(admissions)`; `await progress.stop()` takes executeReturnHops (tool.ts run).
	if x.prog.idle() && r.finalResultPure(x.tool, x.result) {
		// Nothing waits for a progress commit and afterTool has no handler: the stop, the walk and the settled result run no host
		// code and no job, and nothing they read changes in the two ticks of `await progress.stop()`, so they run now and
		// those ticks are spent with the rest, on one chain.
		x.stop(executeReturnHops)
		return
	}
	r.hops(executeReturnHops, x.stopNow)
}

func (x *toolExec) stopNow() { x.stop(0) }

// abandoned fails the task of a result the host cannot record or of an aborted call, once the progress commit in flight ended.
func (x *toolExec) abandoned(waiters []func(error)) {
	for _, w := range waiters {
		w(x.err)
	}
	x.r.fail(x.err)
}

func (x *toolExec) stop(before int) {
	x.before = before
	x.prog.stop(x.stopped)
}

func (x *toolExec) stopped(pending []func(error)) {
	x.pending = pending
	x.r.finalResult(x.call, x.tool, x.result, x.rep, x.before, x.finalDone)
}

// finalDone: finalResult is an async function: `await finalResult(...)` resumes one tick after it returns or throws. lead is the ticks
// that passed before it settled and that its caller has not spent.
func (x *toolExec) finalDone(settled *jv.Object, err error, lead int) {
	x.settled, x.err = settled, err
	x.r.hops(lead+finalResultReturnHops, x.resume)
}

func (x *toolExec) resume() {
	if x.err != nil {
		for _, w := range x.pending {
			w(x.err)
		}
		x.r.fail(x.err)
		return
	}
	x.r.settleThen(x.call, x.end, x.build, &x.duration, x.finish)
}

func (x *toolExec) build(*delta.Obj) *jv.Object { return x.settled }

func (x *toolExec) finish() {
	for _, w := range x.pending {
		w(nil)
	}
	x.r.done(nil)
}

// toolProgress is the throttled commits of what a tool reported into its pi.live.tools slot, each writing only what changed
// since the last one. One commit is in flight at a time; the fields after `then` are that commit's.
type toolProgress struct {
	r   *toolRun
	rep *reported
	// enabled is false for a nested call made with progress: false, which commits only its status; its output stays in the result.
	enabled            bool
	writtenText        string
	writtenDiagnostics int
	writtenEpoch       int

	then           func(bytes int, err error)
	bytes          int
	snapshot       BoundedOutput
	details        any
	hasDetails     bool
	epoch          int
	count          int
	added          []any
	detailsChanged bool
}

// publishProgress is the progress of a running tool.
func (r *toolRun) publishProgress(rep *reported) *progress {
	h := r.g.Host
	t := &toolProgress{r: r, rep: rep, enabled: true}
	if off, ok := r.input.Get("progress"); r.isNested() && ok && off == false {
		t.enabled = false
	}
	return newProgress(h, t.write, t.onError, h.Settings().OutputIntervalMs)
}

func (t *toolProgress) write(then func(bytes int, err error)) {
	if !t.enabled {
		then(0, nil)
		return
	}
	rep := t.rep
	// Capture everything synchronously: the tool keeps reporting while the commit is in flight.
	t.snapshot = rep.output.Snapshot()
	t.details, t.hasDetails, t.epoch = rep.details, rep.hasDetails, rep.detailsEpoch
	t.count = len(rep.diagnostics)
	t.added = append([]any{}, rep.diagnostics[t.writtenDiagnostics:t.count]...)
	t.detailsChanged = t.epoch != t.writtenEpoch
	// What the commit writes, as Chord diffs the string: an append, a trim plus an append of what follows the shared
	// part, or the whole window when its bounded overlap search finds nothing.
	bytes := 0
	if t.snapshot.Text != t.writtenText {
		var shared int
		if jv.HasPrefix(t.snapshot.Text, t.writtenText) {
			shared = jv.UTF16Len(t.writtenText)
		} else {
			shared = delta.Overlap(t.writtenText, t.snapshot.Text, 65536)
		}
		bytes += len(jv.SliceFrom(t.snapshot.Text, shared))
	}
	if t.detailsChanged {
		bytes += len(jv.Stringify(t.details)) // an unset value is JSON null
	}
	if len(t.added) > 0 {
		bytes += len(jv.Stringify(t.added))
	}
	t.then, t.bytes = then, bytes
	t.r.rt.CommitDeferred(t.change, t.committed)
}

func (t *toolProgress) change(tx *session.Tx, _ *jv.Object) (*jv.Object, error) {
	live, err := tx.Doc(LiveDef, session.DocArgs{Owner: t.r.conv})
	if err != nil {
		return nil, err
	}
	slot := ToolSlotOf(live, t.r.rt.TaskID())
	if slot == nil {
		return nil, nil
	}
	snapshot := &t.snapshot
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
	if t.detailsChanged && t.hasDetails {
		if err := assignObj(slot, "details", t.details); err != nil {
			return nil, err
		}
	}
	if len(t.added) > 0 {
		if !slot.Has("diagnostics") {
			if err := slot.Set("diagnostics", []any{}); err != nil {
				return nil, err
			}
		}
		if _, err := slot.Arr("diagnostics").Push(t.added...); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (t *toolProgress) committed(err error) {
	if err == nil {
		t.writtenText, t.writtenDiagnostics, t.writtenEpoch = t.snapshot.Text, t.count, t.epoch
	}
	t.then(t.bytes, err)
}

func (t *toolProgress) onError(err error) {
	// Rejections after an abort mark or close are expected; the committed state stays consistent.
	if !t.r.rt.Signal().Aborted() {
		_ = t.r.rt.Report(err)
	}
}

// finalResult is the settled result: the tool's result with the retained output and last details as fallbacks, its
// diagnostics after those reported through the api, afterTool applied, and explicit text bounded, with the Harness's
// truncation diagnostic last.
//
// before is the ticks of the caller's awaits that have not passed yet: only with finalResultPure, which runs it at once, and then
// they are part of lead, the ticks the caller spends before `await finalResult()` resumes.
func (r *toolRun) finalResult(call *jv.Object, tool *Tool, result *jv.Object, rep *reported, before int, then func(settled *jv.Object, err error, lead int)) {
	f := &finalRun{r: r, call: call, tool: tool, rep: rep, then: then}
	if resultContent, ok := arrayField(result, "output"); ok {
		f.content = resultContent
	} else {
		snap := rep.output.Snapshot()
		f.retained = &snap
		f.content = []any{}
		if snap.Text != "" {
			f.content = []any{jobj("type", "text", "text", snap.Text)}
		}
	}
	f.final = result.Shallow()
	f.final.Set("output", f.content)
	if _, ok := result.Get("details"); !ok && rep.hasDetails {
		f.final.Set("details", rep.details)
	}
	diagnostics := append([]any{}, rep.diagnostics...)
	if list, ok := arrayField(result, "diagnostics"); ok {
		diagnostics = append(diagnostics, list...)
	}
	f.final.Set("diagnostics", diagnostics)
	if r.finalResultPure(tool, result) {
		// No handler: hooks.each takes its two ticks (eachHookRaw) and what follows computes the settled result from the values at
		// hand without a host call, so it runs now and the caller spends those ticks with its own: one chain, not three.
		f.settle(nil, before+eachHookNoHandlerHops)
		return
	}
	r.eachHook("afterTool", f.hookPayload, f.hookResult, f.hookDone)
}

// finalRun is one finalResult: the result being built and what follows afterTool shares.
type finalRun struct {
	r        *toolRun
	call     *jv.Object
	tool     *Tool
	rep      *reported
	then     func(settled *jv.Object, err error, lead int)
	harness  []any
	retained *BoundedOutput
	content  []any
	final    *jv.Object
	out      *jv.Object
	bounded  []any
	// lead is the ticks of hooks.each that already passed when settle runs.
	lead int
}

func (f *finalRun) hookPayload() *jv.Object {
	return f.r.hookPayload(jobj("call", f.call, "result", f.final))
}

func (f *finalRun) hookResult(res any) bool {
	if o, ok := res.(*jv.Object); ok {
		f.final = o
	}
	return false
}

func (f *finalRun) hookDone(err error) { f.settle(err, 0) }

// settle is what follows afterTool: lead is the ticks of hooks.each that already passed when it runs.
func (f *finalRun) settle(err error, lead int) {
	f.lead = lead
	if err != nil {
		f.then(nil, err, lead)
		return
	}
	rep := f.rep
	finalContent, _ := arrayField(f.final, "output")
	// The retained output's truncation applies only while afterTool kept that content.
	if f.retained != nil && f.retained.DroppedBytes > 0 && jv.Equal(finalContent, f.content) {
		f.harness = append(f.harness, truncatedDiagnostic(f.retained.DroppedLines, f.retained.DroppedBytes, rep.limits.Retain))
	}
	bounded, droppedBytes, droppedLines := boundContent(finalContent, rep.limits)
	if droppedBytes > 0 {
		f.harness = append(f.harness, truncatedDiagnostic(droppedLines, droppedBytes, rep.limits.Retain))
	}
	f.bounded = bounded
	f.out = f.final.Shallow()
	f.out.Set("output", bounded)
	// The host holds the schema and judges the settled result, after afterTool (structuredOutputError); a result with no
	// structured output from a tool with no schema keeps the contract without asking.
	if _, has := f.out.Get("structuredOutput"); !has && !f.tool.StructuredOutput {
		f.finish("")
		return
	}
	probe := jv.NewObject()
	if value, has := f.out.Get("structuredOutput"); has {
		probe.Set("structuredOutput", value)
	}
	if isError, has := f.out.Get("isError"); has {
		probe.Set("isError", isError)
	}
	f.r.g.Host.CheckStructuredOutput(str(f.call, "name"), probe, f.judged)
}

func (f *finalRun) judged(text string, err error) {
	if err != nil {
		f.then(nil, err, f.lead)
		return
	}
	f.finish(text)
}

func (f *finalRun) finish(broken string) {
	r, out, tool := f.r, f.out, f.tool
	if broken != "" {
		out.Delete("structuredOutput")
		if r.isNested() {
			// A nested call whose structuredOutput breaks the contract gets an error result without it.
			out.Set("isError", true)
			f.harness = append(f.harness, diagnostic("error", "invalid_structured_output", broken))
		} else {
			// The model never sees structured output, so its call stands; the host learns of the broken tool.
			// Only names the extension: a failed resolution leaves the report without the name.
			_ = r.rt.Report(errors.New(broken), session.ReportFrom{Extension: tool.Extension})
		}
	}
	// Programs get the tool's own value: without a schema, its output. The model reads only content, so model-issued calls
	// skip both.
	if r.isNested() && !tool.StructuredOutput {
		out.Set("structuredOutput", outputValue(f.bounded))
	}
	list, _ := arrayField(f.final, "diagnostics")
	// A schema tool's error result without structured output has its text only in the output, which programs never get:
	// it becomes an error diagnostic, as a thrown error's message does, unless one says what went wrong already (Pi 1.2.0
	// tool.ts finalResult :534-546).
	if isError, _ := out.Get("isError"); r.isNested() && tool.StructuredOutput && isError == true {
		if _, has := out.Get("structuredOutput"); !has && !hasErrorDiagnostic(list) && !hasErrorDiagnostic(f.harness) {
			var texts []string
			for _, item := range f.bounded {
				if o, ok := item.(*jv.Object); ok && str(o, "type") == "text" {
					texts = append(texts, str(o, "text"))
				}
			}
			if text := strings.Join(texts, "\n"); text != "" {
				f.harness = append(f.harness, diagnostic("error", "tool_error", text))
			}
		}
	}
	out.Set("diagnostics", append(append([]any{}, list...), f.harness...))
	f.then(out, nil, f.lead)
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
