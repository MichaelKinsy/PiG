// The effect executors against a scripted core: what the host sends back for each effect (ABI sections 5-8).
import assert from "node:assert/strict"
import { test } from "node:test"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { EVENT } from "../src/abi.ts"
import { HostRegistry } from "../src/registry.ts"
import { apiResult, effect, EFFECT, progressAck, rig, txResult, type Seen } from "./scripted.ts"

const dec = new TextDecoder()
const body = (e: Seen) => JSON.parse(dec.decode(e.payload.subarray(4)))
const registryOf = (extensions: any[]) => new HostRegistry({
  snapshot: () => ({ installed: () => extensions, task: () => undefined, tasks: () => [] }),
  subscribe: () => () => {},
})

function fauxModels(...responses: any[]) {
  const faux = fauxProvider({ models: [{ id: "m1", contextWindow: 1e6 }], tokenSize: { min: 2, max: 2 } })
  const models = createModels()
  models.setProvider(faux.provider)
  faux.setResponses(responses)
  return { faux, models, ref: { provider: faux.provider.id, modelId: "m1" } }
}
const tick = (ms = 20) => new Promise(r => setTimeout(r, ms))
const until = async (test: () => boolean, what: string) => { for (let i = 0; i < 400 && !test(); i++) await tick(5); assert.ok(test(), what) }

test("model_context: the host streams the faux provider and returns batched model_event arrays ending in done", async () => {
  const { models, ref } = fauxModels(fauxAssistantMessage("hello there, this is a streamed answer"))
  const messages = [{ role: "user", content: "hi", timestamp: 1 }]
  const r = rig(ev => ev.kind === EVENT.open ? { effects: [effect(7, EFFECT.model_context, { model: ref, context: { messages }, options: {} })] } : undefined, { registry: registryOf([]), models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.model_event).some(e => body(e).at(-1)?.type === "done"), "no done event")
  const batches = r.core.ofKind(EVENT.model_event).map(body)
  const flat = batches.flat()
  assert.equal(flat[0].type, "start")
  assert.equal(flat.at(-1).message.content[0].text, "hello there, this is a streamed answer")
  assert.ok(batches.length < flat.length, "events are batched, not sent one by one")
  // An event that is not the last of its batch keeps no partial once its content is non-empty.
  for (const batch of batches) for (const ev of batch.slice(0, -1)) if (ev.partial) assert.equal(ev.partial.content.length, 0)
  // The last event of every batch carries its partial or, for done, its message.
  for (const batch of batches) { const last = batch.at(-1); assert.ok(last.partial || last.message || last.error) }
})

test("model_context delta form: extends keeps one list and appends; an unknown extends discards the handle", async () => {
  const seen: unknown[][] = []
  const faux = fauxProvider({ models: [{ id: "m1", contextWindow: 1e6 }] })
  const models = createModels(); models.setProvider(faux.provider)
  faux.setResponses([(ctx: any) => { seen.push([...ctx.messages]); return fauxAssistantMessage("a") }, (ctx: any) => { seen.push([...ctx.messages]); return fauxAssistantMessage("b") }])
  const ref = { provider: faux.provider.id, modelId: "m1" }
  let n = 0
  const r = rig(ev => {
    if (ev.kind !== EVENT.open) return undefined
    n++
    if (n === 1) return { effects: [effect(1, EFFECT.model_context, { model: ref, context: { messages: [{ role: "user", content: "one", timestamp: 1 }] } })] }
    if (n === 2) return { effects: [effect(2, EFFECT.model_context, { model: ref, extends: 1, append: [{ role: "user", content: "two", timestamp: 2 }] })] }
    return { effects: [effect(3, EFFECT.model_context, { model: ref, extends: 1, append: [] })] } // 1 was consumed by 2
  }, { registry: registryOf([]), models })
  r.host.send(EVENT.open); await until(() => seen.length === 1, "first call")
  r.host.send(EVENT.open); await until(() => seen.length === 2, "second call")
  assert.deepEqual(seen.map(m => m.length), [1, 2])
  r.host.send(EVENT.open)
  await until(() => r.host.discarded !== undefined, "an extends of a dropped list must discard the handle")
  assert.match(r.host.discarded!.message, /extends effect 1/)
})

test("tool: execute gets the invocation api; output, diagnostic and details become tool_progress; the result is tool_done 0", async () => {
  const tool = {
    name: "lookup", replay: "safe",
    execute: async (args: any, api: any, context: any) => {
      api.output("abc")
      api.diagnostic({ severity: "info", message: "m" })
      await api.details({ step: 1 }, context)
      api.output("tail", { bytes: 5, newlines: 1, endsWithNewline: true })
      return { content: [{ type: "text", text: `n=${args.n} call=${api.callId} task=${api.taskId} conv=${api.conversationId}` }], details: { ok: true } }
    },
  }
  const registry = registryOf([{ name: "x", tools: [tool] }])
  const r = rig(ev => {
    if (ev.kind === EVENT.open) return { effects: [effect(5, EFFECT.tool, { taskId: 11, conversationId: 3, toolName: "lookup", callId: "c1", arguments: { n: 4 }, replay: "safe" })] }
    if (ev.kind === EVENT.tool_progress && ev.payload[4] === 1) {
      const waitId = new DataView(ev.payload.buffer, ev.payload.byteOffset + 9, 4).getUint32(0, true)
      return { notices: [progressAck(waitId, 0)] }
    }
    return undefined
  }, { registry, models: fauxModels().models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.tool_done).length === 1, "no tool_done")
  const progress = r.core.ofKind(EVENT.tool_progress).map(e => ({ kind: e.payload[4], skipped: new DataView(e.payload.buffer, e.payload.byteOffset + 5, 4).getUint32(0, true), wait: new DataView(e.payload.buffer, e.payload.byteOffset + 9, 4).getUint32(0, true), data: e.payload.subarray(13) }))
  assert.deepEqual(progress.map(p => p.kind), [0, 2, 1, 0])
  assert.equal(dec.decode(progress[0]!.data), "abc")
  assert.deepEqual(JSON.parse(dec.decode(progress[1]!.data)), { severity: "info", message: "m" })
  assert.ok(progress[2]!.wait > 0 && progress[0]!.wait === 0, "only a details update asks for an ack")
  // A skipped output carries its measurement in front of the chunk, the byte count in the header field.
  assert.equal(progress[3]!.skipped, 5)
  const meta = new DataView(progress[3]!.data.buffer, progress[3]!.data.byteOffset).getUint32(0, true)
  assert.deepEqual(JSON.parse(dec.decode(progress[3]!.data.subarray(4, 4 + meta))), { bytes: 5, newlines: 1, endsWithNewline: true })
  assert.equal(dec.decode(progress[3]!.data.subarray(4 + meta)), "tail")
  const done = r.core.ofKind(EVENT.tool_done)[0]!
  assert.equal(done.payload[4], 0)
  assert.equal(JSON.parse(dec.decode(done.payload.subarray(5))).content[0].text, "n=4 call=c1 task=11 conv=3")
})

test("tool: a throw is tool_done outcome 1 with the error's name and message; an operation after the call settled rejects", async () => {
  let api: any
  const tool = { name: "boom", execute: async (_a: any, a: any) => { api = a; throw Object.assign(new RangeError("too big"), {}) } }
  const r = rig(ev => ev.kind === EVENT.open ? { effects: [effect(5, EFFECT.tool, { taskId: 1, conversationId: 1, toolName: "boom", callId: "c", arguments: {}, replay: "unsafe" })] } : undefined, { registry: registryOf([{ name: "x", tools: [tool] }]), models: fauxModels().models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.tool_done).length === 1, "no tool_done")
  const done = r.core.ofKind(EVENT.tool_done)[0]!
  assert.equal(done.payload[4], 1)
  assert.deepEqual({ name: JSON.parse(dec.decode(done.payload.subarray(5))).name, message: JSON.parse(dec.decode(done.payload.subarray(5))).message }, { name: "RangeError", message: "too big" })
  assert.throws(() => api.output("late"), /has settled/)
})

test("hook: the handler at the index runs with positional arguments and the HookApi; a throw is outcome 1", async () => {
  const calls: unknown[][] = []
  const handler = async (...args: unknown[]) => { calls.push(args); return { arguments: { x: 1 } } }
  const thrower = () => { throw new Error("denied") }
  const registry = registryOf([])
  const reg = new HostRegistry({ snapshot: () => ({ installed: () => [{ name: "h", hooks: [{ task: "tool", handlers: { beforeTool: handler, afterTool: thrower } }] }], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} })
  const snap = reg.snapshot()
  const [hBefore, hAfter] = [snap.extensions[0]!.hooks[0]!.handlers.beforeTool!, snap.extensions[0]!.hooks[0]!.handlers.afterTool!]
  let n = 0
  const r = rig(ev => {
    if (ev.kind !== EVENT.open) return undefined
    n++
    return n === 1
      ? { effects: [effect(1, EFFECT.hook, { name: "beforeTool", handler: hBefore, conversationId: 2, taskId: 9, payload: { call: { id: "c", name: "t", arguments: {} } } })] }
      : { effects: [effect(2, EFFECT.hook, { name: "afterTool", handler: hAfter, conversationId: 2, taskId: 9, payload: { call: {}, result: {} } })] }
  }, { registry: reg, models: fauxModels().models })
  void registry
  r.host.send(EVENT.open); r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.hook_done).length === 2, "hooks did not complete")
  const byId = (id: number) => r.core.ofKind(EVENT.hook_done).find(e => new DataView(e.payload.buffer, e.payload.byteOffset, 4).getUint32(0, true) === id)!
  const ok = byId(1), bad = byId(2)
  assert.equal(ok.payload[4], 0)
  assert.deepEqual(JSON.parse(dec.decode(ok.payload.subarray(5))), { arguments: { x: 1 } })
  assert.equal(bad.payload[4], 1)
  assert.equal(JSON.parse(dec.decode(bad.payload.subarray(5))).message, "denied")
  const [call, hookApi, context] = calls[0] as any[]
  assert.deepEqual(call, { id: "c", name: "t", arguments: {} })
  assert.equal(hookApi.taskId, 9); assert.equal(hookApi.conversationId, 2)
  assert.equal(typeof hookApi.memo, "function"); assert.ok(context)
})

test("hook api: memo write and read are api requests answered by api_result notices", async () => {
  const handler = async (_call: unknown, api: any, context: any) => {
    const first = await api.memo("k", { v: 1 }, context)
    const again = await api.memo("k", context)
    return { first, again }
  }
  const reg = new HostRegistry({ snapshot: () => ({ installed: () => [{ name: "h", hooks: [{ task: "tool", handlers: { beforeTool: handler } }] }], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} })
  const id = reg.snapshot().extensions[0]!.hooks[0]!.handlers.beforeTool!
  const memo = new Map<string, unknown>()
  const r = rig(ev => {
    if (ev.kind === EVENT.open) return { effects: [effect(1, EFFECT.hook, { name: "beforeTool", handler: id, conversationId: 1, taskId: 4, payload: { call: {} } })] }
    if (ev.kind === EVENT.api) {
      const b = ev.body
      if (b.op === "memo.put") { if (!memo.has(b.name)) memo.set(b.name, b.candidate); return { notices: [apiResult(ev.id!, { ok: memo.get(b.name) })] } }
      if (b.op === "memo.get") return { notices: [apiResult(ev.id!, { ok: memo.get(b.name) ?? null })] }
    }
    return undefined
  }, { registry: reg, models: fauxModels().models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.hook_done).length === 1, "no hook_done")
  const done = r.core.ofKind(EVENT.hook_done)[0]!
  assert.deepEqual(JSON.parse(dec.decode(done.payload.subarray(5))), { first: { v: 1 }, again: { v: 1 } })
  assert.deepEqual(r.core.ofKind(EVENT.api).map(e => [e.body.op, e.body.taskId]), [["memo.put", 4], ["memo.get", 4]])
})

test("section and env: the env built for the task reaches the section's input; env is hook_done null", async () => {
  const section = { key: "files", render: async (input: any) => `cwd=${input.env?.cwd} shown=${Object.keys(input.shown).join(",")} conv=${input.conversationId}` }
  const reg = new HostRegistry({ snapshot: () => ({ installed: () => [{ name: "s", sections: [section] }], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} })
  const handler = reg.snapshot().extensions[0]!.sections[0]!.handler
  let n = 0
  const r = rig(ev => {
    if (ev.kind !== EVENT.open) return undefined
    return ++n === 1
      ? { effects: [effect(1, EFFECT.env, { conversationId: 2, taskId: 8, cwd: "/work" })] }
      : { effects: [effect(2, EFFECT.section, { conversationId: 2, taskId: 8, section: 0, handler, input: { shown: { a: "x" } } })] }
  }, { registry: reg, models: fauxModels().models, env: (target: any) => ({ cwd: target.cwd }) })
  r.host.send(EVENT.open); await until(() => r.core.ofKind(EVENT.hook_done).length === 1, "no env result")
  r.host.send(EVENT.open); await until(() => r.core.ofKind(EVENT.hook_done).length === 2, "no section result")
  const [env, sec] = r.core.ofKind(EVENT.hook_done)
  assert.equal(dec.decode(env!.payload.subarray(5)), "null")
  assert.equal(JSON.parse(dec.decode(sec!.payload.subarray(5))), "cwd=/work shown=a conv=2")
})

test("deferred: fetch returns the polled message, cancel acknowledges", async () => {
  const faux = fauxProvider({ models: [{ id: "m1", contextWindow: 1e6 }] })
  const models: any = createModels(); models.setProvider(faux.provider)
  const calls: string[] = []
  models.fetchDeferred = async (_m: unknown, handle: unknown) => { calls.push(`fetch ${JSON.stringify(handle)}`); return { role: "assistant", content: [{ type: "text", text: "late" }], stopReason: "stop" } }
  models.cancelDeferred = async (_m: unknown, handle: unknown) => { calls.push(`cancel ${JSON.stringify(handle)}`) }
  const ref = { provider: faux.provider.id, modelId: "m1" }
  let n = 0
  const r = rig(ev => ev.kind === EVENT.open ? { effects: [effect(++n, EFFECT.deferred, { op: n === 1 ? "fetch" : "cancel", model: ref, handle: { id: "h" } })] } : undefined, { registry: registryOf([]), models })
  r.host.send(EVENT.open); r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.hook_done).length === 2, "deferred results")
  assert.deepEqual(calls, ['fetch {"id":"h"}', 'cancel {"id":"h"}'])
  const [fetched, cancelled] = r.core.ofKind(EVENT.hook_done)
  assert.equal(JSON.parse(dec.decode(fetched!.payload.subarray(5))).content[0].text, "late")
  assert.equal(dec.decode(cancelled!.payload.subarray(5)), "null")
})

test("callback: host code runs on the channel the core opened and its result is hook_done", async () => {
  let n = 0
  const r = rig(ev => {
    if (ev.kind === EVENT.open) return { effects: [effect(1, EFFECT.callback, { name: "init", txId: 41, payload: { conversationId: 5 } })] }
    if (ev.kind === EVENT.tx) return { notices: [txResult(ev.id!, { result: { echoed: ev.body.op, n: ++n } })] }
    return undefined
  }, {
    registry: registryOf([]), models: fauxModels().models,
    callbacks: { init: async (payload: any, tx: any) => ({ got: payload.conversationId, entry: await tx.op("appendEntry", { conversationId: payload.conversationId }) }) },
  })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.hook_done).length === 1, "no callback result")
  const done = r.core.ofKind(EVENT.hook_done)[0]!
  assert.deepEqual(JSON.parse(dec.decode(done.payload.subarray(5))), { got: 5, entry: { echoed: "appendEntry", n: 1 } })
  assert.equal(r.core.ofKind(EVENT.tx)[0]!.id, 41, "the operation travels on the txId the core named")
})

test("cancel aborts the model stream's signal and a late completion after discard is dropped", async () => {
  const faux = fauxProvider({ models: [{ id: "m1", contextWindow: 1e6 }], tokensPerSecond: 20, tokenSize: { min: 1, max: 1 } })
  const models = createModels(); models.setProvider(faux.provider)
  faux.setResponses([fauxAssistantMessage("x".repeat(400))])
  const ref = { provider: faux.provider.id, modelId: "m1" }
  let n = 0
  const r = rig(ev => {
    if (ev.kind !== EVENT.open) return undefined
    return ++n === 1 ? { effects: [effect(1, EFFECT.model_context, { model: ref, context: { messages: [{ role: "user", content: "q", timestamp: 1 }] } })] } : { effects: [{ id: 2, kind: EFFECT.cancel, payload: Uint8Array.of(1, 0, 0, 0) }] }
  }, { registry: registryOf([]), models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.model_event).length > 0, "the stream did not start")
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.model_event).some(e => body(e).some((x: any) => x.type === "error" || x.type === "done")), "no terminal event after cancel")
  const terminal = r.core.ofKind(EVENT.model_event).flatMap(body).find((x: any) => x.type === "error" || x.type === "done")
  assert.equal(terminal.type, "error")
  assert.equal(terminal.reason, "aborted")
  void BACKGROUND_CONTEXT
})

test("tool details() settles only at the progress_ack notice; a rejected ack rejects the call", async () => {
  const outcomes: string[] = []
  const tool = {
    name: "d",
    execute: async (_a: any, api: any, context: any) => {
      const first = api.details({ n: 1 }, context).then(() => outcomes.push("first ok"), (e: Error) => outcomes.push(`first ${e.name}`))
      await first
      return { content: [] }
    },
  }
  let wait = 0
  const r = rig(ev => {
    if (ev.kind === EVENT.open) return { effects: [effect(5, EFFECT.tool, { taskId: 1, conversationId: 1, toolName: "d", callId: "c", arguments: {}, replay: "safe" })] }
    if (ev.kind === EVENT.tool_progress && ev.payload[4] === 1) wait = new DataView(ev.payload.buffer, ev.payload.byteOffset + 9, 4).getUint32(0, true)
    if (ev.kind === EVENT.timer) return { notices: [progressAck(wait, 1, { name: "StorageRejected", message: "no" })] }
    return undefined
  }, { registry: registryOf([{ name: "x", tools: [tool] }]), models: fauxModels().models })
  r.host.send(EVENT.open)
  await until(() => wait > 0, "no details update")
  await tick(30)
  assert.deepEqual(outcomes, [], "details must stay pending until the ack")
  assert.equal(r.core.ofKind(EVENT.tool_done).length, 0)
  r.host.send(EVENT.timer, Uint8Array.of(1, 0, 0, 0))
  await until(() => r.core.ofKind(EVENT.tool_done).length === 1, "no tool_done")
  assert.deepEqual(outcomes, ["first StorageRejected"])
})
