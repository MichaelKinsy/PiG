// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// The nested-call half of src/harness/tool.ts: a tool calls another tool through executeTool. The host admits the nested call
// (its tool task, owned by the caller, and its document in the caller's pi.tool.nested-call family); this file is the machine's side: the
// documents, the nested call's result, and the caller's settlement of the calls it left running.

// NestedCallDef is the pi.tool.nested-call family of a calling task, keyed by the nested call's key: so a rerun finds the calls
// it already made, and the caller reads each result. Created with the nested task; the nested call's terminal commit adds its
// result. One document per call, so no commit rewrites the others. It retires with the caller.
// Read-only: assigned once at initialization.
var NestedCallDef = &session.DocDef{
	Kind:    "pi.tool.nested-call",
	Version: 1,
	Scope:   session.ScopeTask,
	Family:  true,
	Initial: func(any) *jv.Object { return jv.NewObject() },
}

// madeNestedCalls is the memo of a tool task that made a nested call, in any attempt, committed before the first admission of
// each attempt: only then can pi.live hold slots below it, which settling removes.
const madeNestedCalls = "pi.tool.madeNestedCalls"

// Ticks of the awaits in tool.ts settle, measured against Pi 1.2.0. `await runtime.ownedTasks()` resumes one tick after the read
// job's end (the line's tail reaction): ownedTasks returns `#read(...)`, an async function returning readOnLine's run promise,
// which it adopts as it is called, so it settles with the tail's reaction and the await resumes one tick later. `await
// runtime.memo(...)` of a #read over an async arrow is three; `await Promise.all(...)` is one (a sibling's last cancel, or at once
// for none).
const (
	ownedTasksResumeTicks = 1
	memoResumeTicks       = 3
	promiseAllResumeTicks = 1
)

// maxSummaryErrorChars is the error text a nested call's slot summary keeps.
const maxSummaryErrorChars = 500

// isNested is whether the task is a nested call: one a running tool made through executeTool.
func (r *toolRun) isNested() bool { return str(r.input, "kind") == "nested" }

// nestedCallIds are the live nested tool calls the task owns, in task ID order: also those an earlier attempt made and this
// one never called again (tool.ts settle, over TaskRuntime.ownedTasks).
func (r *toolRun) nestedCallIds(then func([]ID)) {
	r.rt.OwnedTasks(func(records []*jv.Object, err error) {
		if err != nil {
			r.fail(err)
			return
		}
		var ids []ID
		for _, record := range records {
			if str(record, "kind") == ToolDef.Name && str(objField(record, "input"), "kind") == "nested" {
				ids = append(ids, idField(record, "id"))
			}
		}
		then(ids)
	})
}

// madeNested is whether the tool task made a nested call in any attempt.
func (r *toolRun) madeNested() bool {
	value, found, _ := r.rt.MemoGet(madeNestedCalls)
	return found && value == true
}

// abortAll aborts every nested call of the list and returns once they are all terminal. It marks them all before waiting for
// any: one may run until a sibling is cancelled. ids is not empty: settleThen takes the call without nested calls on one chain.
func (r *toolRun) abortAll(ids []ID, then func()) {
	remaining := len(ids)
	var first error
	for _, id := range ids {
		r.rt.AbortOwned(id, func(err error) {
			if err != nil && first == nil {
				first = err
			}
			remaining--
			if remaining > 0 {
				return
			}
			if first != nil {
				r.fail(first)
				return
			}
			// Promise.all resolves a tick after its last member, and the await resumes a tick after that.
			r.hops(promiseAllResumeTicks+1, then)
		})
	}
}

// nestedResultOf is a nested call's result, as stored and returned: strict JSON, without content, which only the model reads,
// and without control, which only model-issued calls apply.
func nestedResultOf(taskID ID, result *jv.Object, durationMs *float64) *jv.Object {
	out := jobj("taskId", float64(taskID))
	if value, ok := result.Get("structuredOutput"); ok {
		out.Set("structuredOutput", jv.Clone(value))
	}
	isError, _ := result.Get("isError")
	out.Set("isError", isError == true)
	if details, ok := result.Get("details"); ok {
		out.Set("details", jv.Clone(details))
	}
	diagnostics, ok := arrayField(result, "diagnostics")
	if !ok {
		diagnostics = []any{}
	}
	out.Set("diagnostics", jv.Clone(diagnostics))
	if usage := objField(result, "usage"); usage != nil {
		out.Set("usage", jv.Clone(usage))
	}
	if durationMs != nil {
		out.Set("durationMs", *durationMs)
	}
	return out
}

// storeNestedResult adds a nested call's result to its document in its caller's pi.tool.nested-call, in the nested call's
// terminal commit.
func (r *toolRun) storeNestedResult(tx *session.Tx, call, nested *jv.Object) error {
	parent := idField(r.input, "parent")
	key := str(r.input, "key")
	// The caller is never terminal before its owned nested calls, so its document is open; another task in it is a bug, not a
	// race.
	state, err := tx.Doc(NestedCallDef, session.DocArgs{Owner: parent, Key: key})
	if err != nil {
		return err
	}
	if !sameID(state.Get("taskId"), r.rt.TaskID()) {
		return errors.New("Nested call " + str(call, "id") + " is not in its caller's index")
	}
	return state.Set("result", nested)
}

// outputValue is what programs get of a tool's output when it declares no schema: one text item as its string, one image as
// itself, nothing as "", and anything else as the content list.
func outputValue(content []any) any {
	switch len(content) {
	case 0:
		return ""
	case 1:
		if item, ok := content[0].(*jv.Object); ok && str(item, "type") == "text" {
			return str(item, "text")
		}
		return content[0]
	}
	return content
}

// summaryOf is how a nested call ended, for its slot: an error flag, its time and spend, and its error text, from its
// diagnostics or else its output, bounded.
func summaryOf(nested *jv.Object, content []any) *jv.Object {
	isError, _ := nested.Get("isError")
	summary := jobj("isError", isError == true)
	if durationMs, ok := nested.Get("durationMs"); ok {
		summary.Set("durationMs", durationMs)
	}
	if usage := objField(nested, "usage"); usage != nil {
		summary.Set("usage", jv.Clone(usage))
	}
	if isError == true {
		if text := errorTextOf(nested, content); text != "" {
			units := jv.UTF16Units(text)
			if len(units) > maxSummaryErrorChars {
				text = jv.FromUTF16(units[:maxSummaryErrorChars])
			}
			summary.Set("error", text)
		}
	}
	return summary
}

// errorTextOf is the error messages of a nested result, else the text items of its output, joined with newlines.
func errorTextOf(nested *jv.Object, content []any) string {
	var messages []string
	list, _ := arrayField(nested, "diagnostics")
	for _, d := range list {
		if o, ok := d.(*jv.Object); ok && str(o, "severity") == "error" {
			messages = append(messages, str(o, "message"))
		}
	}
	if len(messages) > 0 {
		return joinLines(messages)
	}
	var texts []string
	for _, item := range content {
		if o, ok := item.(*jv.Object); ok && str(o, "type") == "text" {
			texts = append(texts, str(o, "text"))
		}
	}
	return joinLines(texts)
}

func joinLines(parts []string) string {
	var out strings.Builder
	for i, part := range parts {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(part)
	}
	return out.String()
}

// finishNestedSlot marks a nested call's slot done with its summary.
func finishNestedSlot(slot *delta.Obj, summary *jv.Object) error {
	if err := FinishSlot(slot); err != nil {
		return err
	}
	return slot.Set("summary", summary)
}

// hookPayload adds the call's parent to a tool hook's payload when the call is nested, as ToolHookCall.parent.
func (r *toolRun) hookPayload(payload *jv.Object) *jv.Object {
	if r.isNested() {
		payload.Set("parent", jobj("taskId", float64(idField(r.input, "parent")), "callId", str(r.input, "parentCallId")))
	}
	return payload
}
