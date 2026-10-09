// SPDX-License-Identifier: MIT

// Package spec holds the normative tables of ABI 1 (docs/plan/durable-core/ABI.md sections 4-8) and computes abi_id, the
// sha256 of their canonical rendering. It is build tooling, not core: the core carries the result as the constant
// abi.ID, which go generate ./durable/core/abi writes and this package's test checks.
package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// Table row types: name, number, layout.
type kindRow struct {
	Name   string
	Number uint8
	Layout string
}

var eventRows = []kindRow{
	{"open", uint8(abi.EventOpen), "JSON OpenOptions"},
	{"rows", uint8(abi.EventRows), "u16 nReads, (u32 readId, u32 nRows, u8 nCols, value{nRows*nCols})*"},
	{"submit", uint8(abi.EventSubmit), "JSON SubmitEvent"},
	{"model_event", uint8(abi.EventModelEvent), "u32 effectId, JSON array of AssistantMessageEvent"},
	{"model_bytes", uint8(abi.EventModelBytes), "u32 effectId, u8 phase, bytes"},
	{"tool_progress", uint8(abi.EventToolProgress), "u32 effectId, u8 kind, u32 skipped, u32 waitId, bytes"},
	{"tool_done", uint8(abi.EventToolDone), "u32 effectId, u8 outcome, JSON"},
	{"hook_done", uint8(abi.EventHookDone), "u32 effectId, u8 outcome, JSON (completes hook, env, section, deferred and callback effects)"},
	{"phase_done", uint8(abi.EventPhaseDone), "u32 effectId, u8 outcome, JSON"},
	{"tx", uint8(abi.EventTx), "u32 txId, JSON op"},
	{"timer", uint8(abi.EventTimer), "u32 timerId"},
	{"abort", uint8(abi.EventAbort), "JSON {taskId} or {conversationId}"},
	{"registry", uint8(abi.EventRegistry), "JSON registry snapshot"},
	{"api", uint8(abi.EventAPI), "u32 requestId, JSON"},
	{"close", uint8(abi.EventClose), "none"},
	{"inspect", uint8(abi.EventInspect), "JSON query"},
}

var effectRows = []kindRow{
	{"timer", uint8(abi.EffectTimer), "u32 timerId, f64 at, u8 durable"},
	{"timer_clear", uint8(abi.EffectTimerClear), "u32 timerId"},
	{"model_context", uint8(abi.EffectModelContext), "JSON ModelContextEffect: {model, context:{messages}, options} or {model, extends, append, options}"},
	{"model_http", uint8(abi.EffectModelHTTP), "JSON head, body recipe"},
	{"cancel", uint8(abi.EffectCancel), "u32 effectId"},
	{"tool", uint8(abi.EffectTool), "JSON ToolEffect"},
	{"hook", uint8(abi.EffectHook), "JSON {name, handler, conversationId, taskId, payload}"},
	{"phase", uint8(abi.EffectPhase), "JSON {taskId, kind, phase, record, invocation}"},
	{"env", uint8(abi.EffectEnv), "JSON {conversationId}"},
	{"liveness", uint8(abi.EffectLiveness), "f64 at or -1"},
	{"section", uint8(abi.EffectSection), "JSON {conversationId, taskId, section, input}"},
	{"deferred", uint8(abi.EffectDeferred), "JSON {op, model, handle}"},
	{"callback", uint8(abi.EffectCallback), "JSON {name, txId, payload}"},
}

var noticeRows = []kindRow{
	{"published", uint8(abi.NoticePublished), "JSON"},
	{"api_result", uint8(abi.NoticeAPIResult), "u32 requestId, JSON"},
	{"tx_result", uint8(abi.NoticeTxResult), "u32 txId, JSON"},
	{"report", uint8(abi.NoticeReport), "JSON"},
	{"sidecar", uint8(abi.NoticeSidecar), "JSON {conversationId, rows}"},
	{"progress_ack", uint8(abi.NoticeProgressAck), "u32 waitId, u8 outcome, JSON error when rejected"},
}

var statusRows = []kindRow{
	{"ok", uint8(abi.StatusOK), "step applies"},
	{"rejected", uint8(abi.StatusRejected), "no state changed; error is Pi's error name and message"},
	{"fatal", uint8(abi.StatusFatal), "discard the handle"},
}

var valueRows = []kindRow{
	{"null", uint8(abi.ValueNull), "no payload"},
	{"i64", uint8(abi.ValueInt), "8 bytes little endian"},
	{"f64", uint8(abi.ValueFloat), "8 bytes little endian"},
	{"text", uint8(abi.ValueText), "u32 len, UTF-8"},
	{"blob", uint8(abi.ValueBlob), "u32 len, bytes"},
}

type exportRow struct{ Name, Signature string }

// The exports section 4.1 lists. No other export is part of ABI 1.
var exportRows = []exportRow{
	{"_initialize", "()"},
	{"abi_id", "() -> ptr"},
	{"sql_table", "() -> ptr"},
	{"session_new", "() -> u32"},
	{"session_free", "(h u32)"},
	{"in_reserve", "(n u32) -> ptr"},
	{"step", "(h u32, kind u32, len u32, now f64) -> u32"},
	{"out_ptr", "() -> ptr"},
	{"mem_stats", "(h u32) -> ptr"},
}

// Imports ABI section 4.2 lists. The wasi_snapshot_preview1 set is the closed set the host provides: ten functions a minimal Go
// wasip1 reactor links, and the six fd_* functions that importing package os adds (measured with Go 1.27.1; tools/wasm-imports.mjs
// lists the imports of any module and fails on one outside this table). pig.uuidv7 is the only semantic import.
var importRows = []exportRow{
	{"wasi_snapshot_preview1.args_get", "(argv, buf) -> errno"},
	{"wasi_snapshot_preview1.args_sizes_get", "(argc, size) -> errno"},
	{"wasi_snapshot_preview1.clock_time_get", "(id, precision, out) -> errno"},
	{"wasi_snapshot_preview1.environ_get", "(env, buf) -> errno"},
	{"wasi_snapshot_preview1.environ_sizes_get", "(count, size) -> errno"},
	{"wasi_snapshot_preview1.fd_close", "(fd) -> errno"},
	{"wasi_snapshot_preview1.fd_fdstat_get", "(fd, buf) -> errno"},
	{"wasi_snapshot_preview1.fd_fdstat_set_flags", "(fd, flags) -> errno"},
	{"wasi_snapshot_preview1.fd_prestat_dir_name", "(fd, path, len) -> errno"},
	{"wasi_snapshot_preview1.fd_prestat_get", "(fd, buf) -> errno"},
	{"wasi_snapshot_preview1.fd_read", "(fd, iovs, n, nread) -> errno"},
	{"wasi_snapshot_preview1.fd_write", "(fd, iovs, n, written) -> errno"},
	{"wasi_snapshot_preview1.poll_oneoff", "(in, out, n, nevents) -> errno"},
	{"wasi_snapshot_preview1.proc_exit", "(code)"},
	{"wasi_snapshot_preview1.random_get", "(ptr, len) -> errno"},
	{"wasi_snapshot_preview1.sched_yield", "() -> errno"},
	{"pig.uuidv7", "(dst ptr)"},
}

type apiRow struct{ Op, Args, Result, Answered string }

// The api request table (ABI section 8.1): every method of TaskRuntime, ToolExecutionApi, HookApi and Harness that needs core state.
// An api event is `u32 requestId, JSON {op, ...args}`; the answer is an api_result notice `u32 requestId, JSON {ok} or {error:{name,message}}`.
var apiRows = []apiRow{
	{"memo.get", "taskId, name", "the memo value or null", "at once"},
	{"memo.put", "taskId, name, candidate", "the durable winner", "after the memo's commit is applied"},
	{"snapshot", "doc", "the document state or null", "at once"},
	{"snapshotAsOf", "doc, seq", "the document state at seq or null", "at once"},
	{"watch.open", "doc or graph, watchId", "the first frame", "at once; later frames arrive as published notices carrying watchId"},
	{"watch.close", "watchId", "null", "at once"},
	{"getTask", "taskId", "the task record or null", "at once"},
	{"waitForTask", "taskId", "the settled receipt", "when the task is terminal and published"},
	{"outcomes", "taskIds", "the outcomes in order, or an error naming the missing one", "at once"},
	{"entry", "conversationId, entryId", "the entry record or null", "at once"},
	{"context", "conversationId, at", "the ContextView", "at once"},
	{"conversation", "conversationId", "the conversation handle data or null", "at once"},
	{"agent", "conversationId", "the resolved agent", "at once"},
	{"sleep", "taskId, until", "null", "at the wake of the durable timer the core arms"},
	{"usage", "", "the UsageState of the Session", "at once"},
	{"taskGraph", "", "the TaskGraph", "at once"},
}

// The layout lines of sections 4.4 and 4.5.
var layoutLines = []string{
	"step := u32 size, u8 status, u16 nCommits, u16 nReads, u16 nNotices, u16 nEffects, commit*, read*, notice*, effect*, [status != 0: u32 len, error JSON]",
	"size and len count the bytes after their own field; the step export returns size + 4",
	"commit := u32 len, i64 seq, u16 nStmts, stmt*",
	"stmt := u16 sqlId, u8 nParams, value*",
	"read := u32 readId, u16 sqlId, u8 nParams, value*",
	"notice := u8 kind, u32 len, payload",
	"effect := u32 effectId, u8 kind, u32 len, payload",
	"rows-event := u16 nReads, (u32 readId, u32 nRows, u8 nCols, value{nRows*nCols})*",
	"model_bytes phase: 0 head, 1 chunk, 2 end, 3 error",
	"tool_progress waitId is always present: nonzero only for kind 1, whose details update is answered by a progress_ack notice",
	"tool_progress kind: 0 output, 1 details, 2 diagnostic",
	"outcome: 0 result, 1 thrown",
	"memStats: 8 x u64: arena, index, derived, documents, scratch, total live, heap in use, linear memory size",
}

// Canonical renders every normative table in a fixed order, one line per row.
func Canonical() string {
	var b strings.Builder
	section := func(name string, rows []kindRow) {
		fmt.Fprintf(&b, "[%s]\n", name)
		for _, r := range rows {
			fmt.Fprintf(&b, "%d %s: %s\n", r.Number, r.Name, r.Layout)
		}
	}
	b.WriteString("[exports]\n")
	for _, r := range exportRows {
		fmt.Fprintf(&b, "%s %s\n", r.Name, r.Signature)
	}
	b.WriteString("[imports]\n")
	for _, r := range importRows {
		fmt.Fprintf(&b, "%s %s\n", r.Name, r.Signature)
	}
	section("values", valueRows)
	section("status", statusRows)
	section("events", eventRows)
	section("effects", effectRows)
	section("notices", noticeRows)
	b.WriteString("[api]\n")
	for _, r := range apiRows {
		fmt.Fprintf(&b, "%s(%s) -> %s; answered %s\n", r.Op, r.Args, r.Result, r.Answered)
	}
	b.WriteString("[layout]\n")
	for _, l := range layoutLines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// ID is the abi_id every core exports: the hex sha256 of Canonical.
func ID() string {
	sum := sha256.Sum256([]byte(Canonical()))
	return hex.EncodeToString(sum[:])
}

// IDSource renders durable/core/abi/id.go.
func IDSource() string {
	return "// SPDX-License-Identifier: MIT\n\n" +
		"// Code generated by go generate ./durable/core/abi; DO NOT EDIT.\n\n" +
		"package abi\n\n" +
		"//go:generate go run ./spec/cmd/abigen -dir .\n\n" +
		"// ID is abi_id: the hex sha256 of the canonical rendering of the ABI 1 tables in package spec.\n" +
		"const ID = \"" + ID() + "\"\n"
}

// EventKindByName resolves an event name of the section 5 table.
func EventKindByName(name string) (abi.EventKind, bool) {
	for _, r := range eventRows {
		if r.Name == name {
			return abi.EventKind(r.Number), true
		}
	}
	return 0, false
}

// Imports returns the module.name of every import the host provides (section 4.2).
func Imports() []string {
	out := make([]string, len(importRows))
	for i, r := range importRows {
		out[i] = r.Name
	}
	return out
}

// Exports returns the name of every export of section 4.1.
func Exports() []string {
	out := make([]string, len(exportRows))
	for i, r := range exportRows {
		out[i] = r.Name
	}
	return out
}
