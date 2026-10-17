// watchEvents over the core's view frames: translate() of src/harness/events.ts, driven by scripted frames.
import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry, Harness, MemoryStorage, watchEvents } from "../src/facade/index.ts"
import { EVENT } from "../src/abi.ts"
import { apiResult, published, ScriptedCore, type Seen } from "./scripted.ts"

const context = BACKGROUND_CONTEXT
const models = () => { const m = createModels(); m.setProvider(fauxProvider().provider); return m as any }
const tick = () => new Promise(resolve => setTimeout(resolve, 5))

const view = (docs: Record<string, unknown> = {}, entries: unknown[] = []) => ({ conversation: { id: 1 }, entries, docs })
const none = { entries: [], tasks: [], submissions: [] }

async function stream(first: unknown, frames: (watchId: number) => any[]) {
  let watchId = 0
  const core = new ScriptedCore((ev: Seen) => {
    if (ev.kind === EVENT.api && ev.body.op === "watch.open") { watchId = ev.body.watchId; return { notices: [apiResult(ev.id!, { ok: { value: first } })] } }
    if (ev.kind === EVENT.api && ev.body.op === "watch.close") return { notices: [apiResult(ev.id!, { ok: null })] }
    if (ev.kind === EVENT.registry) return { notices: frames(watchId).map(frame => published({ watchId, frame })) }
    return undefined
  })
  const h = await Harness.open(new MemoryStorage(), { models: models(), registry: createRegistry(), core: { newSession: () => core, sqlTable: [] } } as any, context)
  ;(h as any).completingGenerations = () => []
  const events = await watchEvents(h, 1 as never, context)
  const seen: any[][] = []
  events.start(async list => { seen.push([...list]) })
  const pump = async () => { (h as any).host.sendJSON(EVENT.registry, {}); await tick(); await tick() }
  return { events, seen, pump, core }
}

test("the snapshot is the view: entries, run, tools, nestedTools (events.ts snapshotOf), inbox, agent and usage, with the documents' initial values when absent", async () => {
  const { events } = await stream(view({ "pi.live": { run: { taskId: 9, inputs: [8] }, tools: [{ callId: "c", name: "t", status: "running" }], nestedTools: [{ callId: "n", taskId: 12, parentCallId: "c", parentTaskId: 11, name: "t", status: "running" }] }, "pi.inbox": { items: [{ id: 10, mode: "followUp", content: "x" }] } }, [{ id: 7, kind: "pi.user" }]), () => [])
  assert.deepEqual(events.snapshot, {
    type: "snapshot", entries: [{ id: 7, kind: "pi.user" }], run: { inputs: [8] }, tools: [{ callId: "c", name: "t", status: "running" }],
    nestedTools: [{ callId: "n", taskId: 12, parentCallId: "c", parentTaskId: 11, name: "t", status: "running" }], compactions: [], inbox: [{ id: 10, mode: "followUp" }], agent: {}, usage: { models: {}, tools: {} },
  })
})

test("a commit that starts a run places the user entry, the submission and starts the turn", async () => {
  const entry = { id: 7, kind: "pi.user", model: [{ role: "user", content: "hi", timestamp: 1 }] }
  const task = { id: 9, kind: "pi.generation", conversationId: 1, state: { status: "pending" } }
  const submission = { id: 8, conversationId: 1, status: "placed", entry: 7 }
  const { seen, pump } = await stream(view({ "pi.live": {} }), () => [{
    ops: [["s", ["docs", "pi.live", "run"], { taskId: 9, inputs: [8] }], ["p", ["entries"], 0, 0, [entry]]],
    changes: { entries: [entry], tasks: [task], submissions: [submission] },
  }])
  await pump()
  assert.deepEqual(seen[0]!.map(e => e.type), ["message_start", "message_end", "submission", "run_start", "turn_start"])
  assert.deepEqual(seen[0]![1], { type: "message_end", entry })
  assert.deepEqual(seen[0]![2], { type: "submission", record: submission })
})

test("a streamed assistant partial becomes message_start then text deltas; the final entry ends the message and the turn ends when the generation completes", async () => {
  const partial = (text: string) => ({ role: "assistant", content: [{ type: "text", text }], usage: { input: 1 } })
  const assistant = { id: 12, kind: "pi.assistant", model: [{ ...partial("hello"), stopReason: "stop" }] }
  const { seen, pump } = await stream(view({ "pi.live": { run: { taskId: 9, inputs: [8] } } }), () => [
    { ops: [["s", ["docs", "pi.live", "generation"], { attempt: 1, message: partial("hel") }]], changes: none },
    { ops: [["a", ["docs", "pi.live", "generation", "message", "content", 0, "text"], "lo"]], changes: none },
    { ops: [["d", ["docs", "pi.live", "generation"]], ["p", ["entries"], 0, 0, [assistant]]], changes: { entries: [assistant], tasks: [{ id: 9, kind: "pi.generation", conversationId: 1, state: { status: "completing" } }], submissions: [] } },
  ])
  await pump()
  const types = seen.flat().map(e => e.type)
  assert.deepEqual(types.slice(0, 2), ["message_start", "message_update"])
  assert.deepEqual(seen.flat()[1].changes, [{ type: "text_delta", contentIndex: 0, delta: "lo" }])
  assert.deepEqual(types.slice(-2), ["message_end", "turn_end"], "the entry ends the streamed message without a second message_start")
  assert.equal(types.filter(t => t === "message_start").length, 1)
})

test("the snapshot carries pi.live.compactionFailure (Pi 89e5c2c0)", async () => {
  const failure = { taskId: 5, reason: "threshold", error: "Summarization failed: bad request" }
  const { events } = await stream(view({ "pi.live": { compactionFailure: failure } }), () => [])
  assert.deepEqual((events.snapshot as any).compactionFailure, failure)
  const clean = await stream(view({ "pi.live": {} }), () => [])
  assert.equal("compactionFailure" in clean.events.snapshot, false, "absent when the live document has none")
})

test("compaction_end carries the error of the compaction that failed, and none when the task produced a summary (Pi 89e5c2c0)", async () => {
  const failure = { taskId: 5, reason: "threshold", error: "Summarization failed: bad request" }
  const running = [{ taskId: 5, reason: "threshold", blocking: false }, { taskId: 6, reason: "manual", blocking: true }]
  const { seen, pump } = await stream(view({ "pi.live": { compactions: running } }), () => [
    { ops: [["s", ["docs", "pi.live", "compactions"], [running[1]]], ["s", ["docs", "pi.live", "compactionFailure"], failure]], changes: none },
    { ops: [["s", ["docs", "pi.live", "compactions"], []]], changes: none },
  ])
  await pump()
  const ends = seen.flat().filter(e => e.type === "compaction_end")
  assert.deepEqual(ends, [
    { type: "compaction_end", taskId: 5, reason: "threshold", error: "Summarization failed: bad request" },
    { type: "compaction_end", taskId: 6, reason: "manual" },
  ])
})

// Pi 97195ec3 events.ts callOf: a tool event names the call and its task (and the caller, for a nested call).
test("tool events carry the slot's taskId (callOf), start with the intent's arguments", async () => {
  const task = { id: 12, kind: "pi.tool", conversationId: 1, state: { status: "running", checkpoint: { arguments: { command: "ls" } } } }
  const running = { callId: "c", name: "bash", taskId: 12, status: "running" }
  const { seen, pump } = await stream(view({ "pi.live": {} }), () => [{
    ops: [["s", ["docs", "pi.live", "tools"], [running]]],
    changes: { entries: [], tasks: [task], submissions: [] },
  }])
  await pump()
  assert.deepEqual(seen[0]![0], { type: "tool_execution_start", toolCallId: "c", toolName: "bash", taskId: 12, args: { command: "ls" } })
})


test("the snapshot carries pi.live.compactionFailure (Pi 89e5c2c0)", async () => {
  const failure = { taskId: 5, reason: "threshold", error: "Summarization failed: bad request" }
  const { events } = await stream(view({ "pi.live": { compactionFailure: failure } }), () => [])
  assert.deepEqual((events.snapshot as any).compactionFailure, failure)
  const clean = await stream(view({ "pi.live": {} }), () => [])
  assert.equal("compactionFailure" in clean.events.snapshot, false, "absent when the live document has none")
})

test("compaction_end carries the error of the compaction that failed, and none when the task produced a summary (Pi 89e5c2c0)", async () => {
  const failure = { taskId: 5, reason: "threshold", error: "Summarization failed: bad request" }
  const running = [{ taskId: 5, reason: "threshold", blocking: false }, { taskId: 6, reason: "manual", blocking: true }]
  const { seen, pump } = await stream(view({ "pi.live": { compactions: running } }), () => [
    { ops: [["s", ["docs", "pi.live", "compactions"], [running[1]]], ["s", ["docs", "pi.live", "compactionFailure"], failure]], changes: none },
    { ops: [["s", ["docs", "pi.live", "compactions"], []]], changes: none },
  ])
  await pump()
  const ends = seen.flat().filter(e => e.type === "compaction_end")
  assert.deepEqual(ends, [
    { type: "compaction_end", taskId: 5, reason: "threshold", error: "Summarization failed: bad request" },
    { type: "compaction_end", taskId: 6, reason: "manual" },
  ])
})
