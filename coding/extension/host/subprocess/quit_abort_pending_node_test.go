//go:build !pig_strip_node_extensions

package subprocess

import "testing"

// Pi aborts the run in the tick stdin ends: shutdown() calls runtimeHost.dispose(), whose first statement, session.abort(), fires the run's signal before it
// returns (rpc-mode.ts:744-751, agent-session-runtime.ts:415-424, agent-session.ts:2601-2611). A tool that waits for nothing but that signal is woken before
// the loop turns, so Pi's loop is never empty with it pending. Here the host tells the runtime that input ended when stdin ends and sends the abort after it, so
// the runtime's loop can empty in between, and it must not report the tool as held then: the process would exit with no session_shutdown (the exit of the
// handler the abort would have woken). These tests force that order: the loop empties after runtime_input_end, and the run's abort is delivered later.

// toolWaitingForItsAbort is a tool whose execute waits for nothing but its signal and counts the aborts it saw.
const toolWaitingForItsAbort = `
let woken = 0;
runtime.tools.set("probe", { execute: (_id, _params, signal) => new Promise((resolve) => signal.addEventListener("abort", () => { woken++; resolve({ content: [{ type: "text", text: "aborted" }] }); }, { once: true })) });
`

// toolDeafToItsAbort is a tool that ignores its signal and never settles: it holds no handle.
const toolDeafToItsAbort = `
runtime.tools.set("probe", { execute: () => new Promise(() => {}) });
`

// runSignalAndToolCall puts a run in progress (the host's run_signal frame, as it sends one before every request) and the tool call in flight.
const runSignalAndToolCall = `
const runSignal = (aborted) => feed({ type: "notify", notify: { method: "run_signal", args: { run: 1, active: true, aborted } } });
runSignal(false);
feed({ type: "request", id: "r1", request: { method: "tool_call", tool: "probe", tool_call_id: "call_1", args: {} } });
await turn();
`

// TestNodeRuntimeHoldsBackTheDrainReportOfAToolWaitingForItsAbortUntilTheAbortArrives: the loop is empty after runtime_input_end and the abort has not been
// delivered; the runtime reports its drained loop without a held handler (runtime_commands_drained), leaves the tool running, and keeps reading. The abort then
// wakes the tool, which answers, and nothing reports the tool as held.
func TestNodeRuntimeHoldsBackTheDrainReportOfAToolWaitingForItsAbortUntilTheAbortArrives(t *testing.T) {
	quitRuntimeScript(t, toolWaitingForItsAbort+runSignalAndToolCall+`
inputEnd();
await turn();
loopDrained();
assert.deepEqual(reports(), ["runtime_commands_drained"], "the loop emptied after input ended and the run's abort had not reached the runtime: it reported the tool as held (runtime_drained), and the host would exit without session_shutdown, where Pi's abort wakes the tool first");
assert.equal(woken, 0, "nothing aborted the tool yet");
runSignal(true);
await turn();
assert.equal(woken, 1, "the run's abort did not reach the tool");
assert.ok(frames.some((f) => f.type === "response" && f.id === "r1"), "the tool did not answer after its abort");
loopDrained();
assert.ok(!reports().includes("runtime_drained"), "a tool that answered after its abort was reported as held: " + reports());
`)
}

// TestNodeRuntimeReportsAToolThatIgnoresItsAbortOnceTheAbortArrived: Pi's process exits 0 when its loop empties with a tool that ignores the abort pending
// (Pi 1.5.2: 15 ms over RPC), and the host exits at the runtime's report. The report follows the abort, not input's end.
func TestNodeRuntimeReportsAToolThatIgnoresItsAbortOnceTheAbortArrived(t *testing.T) {
	quitRuntimeScript(t, toolDeafToItsAbort+runSignalAndToolCall+`
inputEnd();
await turn();
loopDrained();
assert.deepEqual(reports(), ["runtime_commands_drained"], "before the abort the runtime reports no held tool");
runSignal(true);
await turn();
loopDrained();
assert.deepEqual(reports(), ["runtime_commands_drained", "runtime_drained"], "after the abort a tool that ignores it left the loop empty: the runtime must report it, as Pi's process exits then");
`)
}

// TestNodeRuntimeReportsAToolAtOnceWhenNoRunIsActive: no run signal, so no abort is on its way: the report is not held back. A tool call outside a run (a nested
// call) is never aborted by the quit, and Pi's process exits when its loop empties.
func TestNodeRuntimeReportsAToolAtOnceWhenNoRunIsActive(t *testing.T) {
	quitRuntimeScript(t, toolDeafToItsAbort+`
feed({ type: "request", id: "r1", request: { method: "tool_call", tool: "probe", tool_call_id: "call_1", args: {} } });
await turn();
inputEnd();
await turn();
loopDrained();
assert.deepEqual(reports(), ["runtime_drained"], "with no run in progress nothing waits for an abort: the tool is held at once");
`)
}

// TestNodeRuntimeReportsAToolWhoseRequestWasCancelledBeforeTheRunsSignal: the host's signal-only cancel of the request is an abort that reached it.
func TestNodeRuntimeReportsAToolWhoseRequestWasCancelledBeforeTheRunsSignal(t *testing.T) {
	quitRuntimeScript(t, toolDeafToItsAbort+runSignalAndToolCall+`
inputEnd();
await turn();
feed({ type: "cancel", id: "r1", cancel: { request_id: "r1", signal_only: true } });
await turn();
loopDrained();
assert.ok(reports().includes("runtime_drained"), "a request cancelled (signal-only) with nothing else to wake it was not reported as held: " + reports());
`)
}
