// The effect executors against a scripted core: what the host sends back for each effect (ABI sections 5-8).
import assert from "node:assert/strict"
import { test } from "node:test"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { EVENT } from "../src/abi.ts"
import { withSignal } from "../src/effects.ts"
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
  // An event that is not the last of its batch keeps no partial once its content is non-empty.
  for (const batch of batches) for (const ev of batch.slice(0, -1)) if (ev.partial) assert.equal(ev.partial.content.length, 0)
  // The last event of every batch carries its partial or, for done, its message.
  for (const batch of batches) { const last = batch.at(-1); assert.ok(last.partial || last.message || last.error) }
})

// Pi consumes the stream in microtasks and arms its partial flush timer (100 ms) when text_start arrives (generation.ts:415-420): the
// host delivers an event in the microtask checkpoint where the stream yielded it, not a macrotask later. A macrotask batch armed
// the flush behind another stream's equal-deadline chunk timer and flipped E23's commit order (c23).
// Pi (generation.ts :201-205) builds the request options as { ...streamOptions, signal, sessionId, ...reasoning }: a provider that
// enumerates or serialises its options sees signal before sessionId.
test("model_context: the provider receives the signal before sessionId, as in Pi's generation", async () => {
  const { models, ref } = fauxModels(fauxAssistantMessage("ok"))
  const seen: string[][] = []
  const call = models.streamSimple.bind(models)
  ;(models as any).streamSimple = (...args: any[]) => { seen.push(Object.keys(args[2])); return (call as any)(...args) }
  const options = { headers: { a: "b" }, sessionId: "s1", reasoning: "low" }
  const r = rig(ev => ev.kind === EVENT.open ? { effects: [effect(7, EFFECT.model_context, { model: ref, context: { messages: [{ role: "user", content: "hi", timestamp: 1 }] }, options })] } : undefined, { registry: registryOf([]), models })
  r.host.send(EVENT.open)
  await until(() => seen.length === 1, "no request")
  assert.deepEqual(seen[0]!.slice(0, 4), ["headers", "signal", "sessionId", "reasoning"])
})

test("withSignal: before sessionId, in place when present, last when there is no sessionId, and the input is not changed", () => {
  const signal = new AbortController().signal
  const keys = (o: Record<string, unknown>) => Object.keys(o).join(",")
  assert.equal(keys(withSignal({ sessionId: "s" }, signal)), "signal,sessionId")
  assert.equal(keys(withSignal({ headers: {}, sessionId: "s", reasoning: "low" }, signal)), "headers,signal,sessionId,reasoning")
  assert.equal(keys(withSignal({ headers: {} }, signal)), "headers,signal")
  assert.equal(keys(withSignal(undefined, signal)), "signal")
  const given = { a: 1, signal: "x", sessionId: "s" }
  const out = withSignal(given, signal)
  assert.equal(keys(out), "a,signal,sessionId")
  assert.equal(out.signal, signal)
  assert.equal(given.signal, "x")
})

test("model_context: the batch holding text_start ends with it (microtask delivery)", async () => {
  const { models, ref } = fauxModels(fauxAssistantMessage("hello there, this is a streamed answer"))
  const r = rig(ev => ev.kind === EVENT.open ? { effects: [effect(7, EFFECT.model_context, { model: ref, context: { messages: [{ role: "user", content: "hi", timestamp: 1 }] }, options: {} })] } : undefined, { registry: registryOf([]), models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.model_event).some(e => body(e).at(-1)?.type === "done"), "no done event")
  const holding = r.core.ofKind(EVENT.model_event).map(body).find(b => b.some((e: any) => e.type === "text_start"))!
  assert.equal(holding.at(-1).type, "text_start", JSON.stringify(holding.map((e: any) => e.type)))
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

// Pi hands every model request its own copy of the message list (generation.ts:402 `{ messages: [...messages] }`). The delta form
// keeps the host's list and grows it for the next request of the conversation: a provider that holds the list of an earlier
// request must not see it grow.
test("model_context delta form: a provider's list of an earlier request does not grow with the next request", async () => {
  const held: unknown[][] = []
  const faux = fauxProvider({ models: [{ id: "m1", contextWindow: 1e6 }] })
  const models = createModels(); models.setProvider(faux.provider)
  faux.setResponses([(ctx: any) => { held.push(ctx.messages); return fauxAssistantMessage("a") }, (ctx: any) => { held.push(ctx.messages); return fauxAssistantMessage("b") }])
  const ref = { provider: faux.provider.id, modelId: "m1" }
  let n = 0
  const r = rig(ev => {
    if (ev.kind !== EVENT.open) return undefined
    n++
    if (n === 1) return { effects: [effect(1, EFFECT.model_context, { model: ref, conversationId: 1, context: { messages: [{ role: "user", content: "one", timestamp: 1 }] } })] }
    return { effects: [effect(2, EFFECT.model_context, { model: ref, conversationId: 1, extends: 1, append: [{ role: "user", content: "two", timestamp: 2 }] })] }
  }, { registry: registryOf([]), models })
  r.host.send(EVENT.open); await until(() => held.length === 1, "first call")
  r.host.send(EVENT.open); await until(() => held.length === 2, "second call")
  assert.deepEqual(held.map(m => m.length), [1, 2], "the first request's list kept its length")
  assert.notEqual(held[0], held[1])
})

// Skipped output requires tail retention (output.ts OutputBuffer.push), so the call carries the window the core offers for a tail tool.
test("tool: execute gets the invocation api; output, diagnostic and details become tool_progress; the result is tool_done 0", async () => {
  const tool = {
    name: "lookup", replay: "safe",
    execute: async (args: any, api: any, context: any) => {
      api.output("abc")
      api.diagnostic({ severity: "info", message: "m" })
      await api.details({ step: 1 }, context)
      api.output("tail", { bytes: 5, newlines: 1, endsWithNewline: true })
      return { output: [{ type: "text", text: `n=${args.n} call=${api.callId} task=${api.taskId} conv=${api.conversationId}` }], details: { ok: true } }
    },
  }
  const registry = registryOf([{ name: "x", tools: [tool] }])
  const r = rig(ev => {
    if (ev.kind === EVENT.open) return { effects: [effect(5, EFFECT.tool, { taskId: 11, conversationId: 3, toolName: "lookup", callId: "c1", arguments: { n: 4 }, outputWindow: { maxBytes: 51200, maxLines: 2000 }, replay: "safe" })] }
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
  assert.equal(JSON.parse(dec.decode(done.payload.subarray(5))).output[0].text, "n=4 call=c1 task=11 conv=3")
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

test("tool: a call whose signal was aborted still reports tool_done, so the core can settle it with its aborted result", async () => {
  let n = 0
  const tool = { name: "wait", execute: (_a: any, _api: any, context: any) => new Promise((_, reject) => context.abortSignal.addEventListener("abort", () => reject(new Error("aborted by the core")))) }
  const r = rig(ev => {
    if (ev.kind !== EVENT.open) return undefined
    return ++n === 1
      ? { effects: [effect(5, EFFECT.tool, { taskId: 1, conversationId: 1, toolName: "wait", callId: "c", arguments: {}, replay: "unsafe" })] }
      : { effects: [{ id: 6, kind: EFFECT.cancel, payload: Uint8Array.of(5, 0, 0, 0) }] }
  }, { registry: registryOf([{ name: "x", tools: [tool] }]), models: fauxModels().models })
  r.host.send(EVENT.open)
  await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(r.core.ofKind(EVENT.tool_done).length, 0, "a running call has not reported")
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.tool_done).length === 1, "a cancelled call must still report")
  const done = r.core.ofKind(EVENT.tool_done)[0]!
  assert.equal(done.payload[4], 1, "outcome 1: thrown")
  assert.equal(JSON.parse(dec.decode(done.payload.subarray(5))).message, "aborted by the core")
})

// A cancel only signals: Pi awaits the hook it signalled (tool.ts:67 beforeTool, scheduler hooks), so the handler's own throw after
// the abort still reaches the core as hook_done 1 and the core settles on it (E23 commit 66 family).
test("hook: a handler whose signal was aborted still reports hook_done, a throw as outcome 1", async () => {
  let n = 0
  const r = rig(ev => {
    if (ev.kind !== EVENT.open) return undefined
    return ++n === 1
      ? { effects: [effect(5, EFFECT.hook, { name: "slow", handler: 0, conversationId: 1, taskId: 1, payload: {} })] }
      : { effects: [{ id: 6, kind: EFFECT.cancel, payload: Uint8Array.of(5, 0, 0, 0) }] }
  }, { registry: registryOf([]), models: fauxModels().models, hostHooks: { slow: (_p: unknown, context: any) => new Promise((_, reject) => context.abortSignal.addEventListener("abort", () => reject(new Error("hook aborted")))) } } as any)
  r.host.send(EVENT.open)
  await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(r.core.ofKind(EVENT.hook_done).length, 0, "a running hook has not reported")
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.hook_done).length === 1, "a cancelled hook must still report")
  const done = r.core.ofKind(EVENT.hook_done)[0]!
  assert.equal(done.payload[4], 1)
  assert.equal(JSON.parse(dec.decode(done.payload.subarray(5))).message, "hook aborted")
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

test("hook: an unknown hook name or an index that holds no function is a protocol failure that discards the handle", async () => {
  const handler = () => undefined
  const reg = new HostRegistry({ snapshot: () => ({ installed: () => [{ name: "h", sections: [{ key: "k", render: () => "" }], hooks: [{ task: "tool", handlers: { beforeTool: handler } }] }], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} })
  const snapshot = reg.snapshot().extensions[0]!
  const id = snapshot.hooks[0]!.handlers.beforeTool!
  for (const [name, effectJson, message] of [
    ["unknown name", { name: "noSuchHook", handler: id, conversationId: 1, taskId: 1, payload: {} }, /unknown hook noSuchHook/],
    ["outside the table", { name: "beforeTool", handler: id + 50, conversationId: 1, taskId: 1, payload: { call: {} } }, /not in the registry table/],
    ["wrong kind (a section)", { name: "beforeTool", handler: snapshot.sections[0]!.handler, conversationId: 1, taskId: 1, payload: { call: {} } }, /is not a function/],
  ] as const) {
    const r = rig(ev => ev.kind === EVENT.open ? { effects: [effect(1, EFFECT.hook, effectJson)] } : undefined, { registry: reg, models: {} as any })
    r.host.send(EVENT.open)
    await until(() => r.host.discarded !== undefined, `${name}: the handle was not discarded`)
    assert.match(r.host.discarded!.message, message)
    assert.equal(r.core.ofKind(EVENT.hook_done).length, 0, `${name}: no outcome is reported for a protocol failure`)
  }
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

test("phase: a handler whose signal was aborted still reports phase_done, so the core can end the invocation", async () => {
  let n = 0
  const r = rig(ev => {
    if (ev.kind !== EVENT.open) return undefined
    return ++n === 1
      ? { effects: [effect(1, EFFECT.phase, { taskId: 7, kind: "k", phase: "p", record: { id: 7 }, invocation: 7 })] }
      : { effects: [{ id: 2, kind: EFFECT.cancel, payload: Uint8Array.of(1, 0, 0, 0) }] }
  }, {
    registry: registryOf([]), models: fauxModels().models,
    phase: (_effect: any, context: any) => new Promise((_, reject) => context.abortSignal.addEventListener("abort", () => reject(new Error("aborted by the core")))),
  })
  r.host.send(EVENT.open)
  await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(r.core.ofKind(EVENT.phase_done).length, 0, "a running handler has not reported")
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.phase_done).length === 1, "an aborted phase handler must still report")
  const done = r.core.ofKind(EVENT.phase_done)[0]!
  assert.equal(done.payload[4], 1, "outcome 1: thrown")
  assert.equal(JSON.parse(dec.decode(done.payload.subarray(5))).message, "aborted by the core")
})

test("tool details() settles only at the progress_ack notice; a rejected ack rejects the call", async () => {
  const outcomes: string[] = []
  const tool = {
    name: "d",
    execute: async (_a: any, api: any, context: any) => {
      const first = api.details({ n: 1 }, context).then(() => outcomes.push("first ok"), (e: Error) => outcomes.push(`first ${e.name}`))
      await first
      return { output: [] }
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

test("tool: the output window is offered as the core configured it, pace included", async () => {
  let seen: unknown
  const tool = { name: "windowed", execute: async (_a: any, api: any) => { seen = api.outputWindow; return { output: [] } } }
  const window = { maxBytes: 51200, maxLines: 1, minIntervalMs: 250, bytesPerSecond: 102400 }
  const r = rig(ev => ev.kind === EVENT.open ? { effects: [effect(5, EFFECT.tool, { taskId: 1, conversationId: 1, toolName: "windowed", callId: "c", arguments: {}, outputWindow: window, replay: "safe" })] } : undefined, { registry: registryOf([{ name: "x", tools: [tool] }]), models: fauxModels().models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.tool_done).length === 1, "no tool_done")
  assert.deepEqual(seen, window)
})

// Pi 97195ec3 tool.ts retainedOutput: the output the call keeps so far as the model will see it, and whether earlier output was dropped.
test("tool: retainedOutput answers in the call's tick from the output pushed so far, bounded by the tool's limits or the core's window", async () => {
  const seen: unknown[] = []
  const tool = { name: "keeper", outputLimits: { maxBytes: 1000, maxLines: 2, retain: "head" }, execute: async (_a: any, api: any) => {
    seen.push(api.retainedOutput())
    api.output("one\n"); api.output(new TextEncoder().encode("two\nthree\n"))
    seen.push(api.retainedOutput())
    return { output: [] }
  } }
  const windowed = { name: "tailer", execute: async (_a: any, api: any) => {
    api.output("a\nb\n"); api.output("c\n", { bytes: 4, newlines: 2, endsWithNewline: true })
    seen.push(api.retainedOutput())
    return { output: [] }
  } }
  const r = rig(ev => ev.kind === EVENT.open ? { effects: [
    effect(5, EFFECT.tool, { taskId: 1, conversationId: 1, toolName: "keeper", callId: "c1", arguments: {}, replay: "safe" }),
    effect(6, EFFECT.tool, { taskId: 2, conversationId: 1, toolName: "tailer", callId: "c2", arguments: {}, outputWindow: { maxBytes: 51200, maxLines: 1 }, replay: "safe" }),
  ] } : undefined, { registry: registryOf([{ name: "x", tools: [tool, windowed] }]), models: fauxModels().models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.tool_done).length === 2, "no tool_done")
  assert.deepEqual(seen, [{ text: "", truncated: false }, { text: "one\ntwo\n", truncated: true }, { text: "c\n", truncated: true }])
})

test("tool: retainedOutput after the call settled throws as every other api call does", async () => {
  let late: any
  const tool = { name: "late", execute: async (_a: any, api: any) => { late = api; return { output: [] } } }
  const r = rig(ev => ev.kind === EVENT.open ? { effects: [effect(5, EFFECT.tool, { taskId: 1, conversationId: 1, toolName: "late", callId: "c", arguments: {}, replay: "safe" })] } : undefined, { registry: registryOf([{ name: "x", tools: [tool] }]), models: fauxModels().models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.tool_done).length === 1, "no tool_done")
  assert.throws(() => late.retainedOutput(), /Tool call c has settled/)
})

test("tool: api.commit begins with the call's invocation and returns the callback's value without making it the task's next state", async () => {
  let returned: unknown
  const tool = { name: "writer", execute: async (_a: any, api: any, context: any) => { returned = await api.commit(async () => ({ entry: 7 }), context); return { output: [] } } }
  const r = rig(ev => {
    if (ev.kind === EVENT.open) return { effects: [effect(5, EFFECT.tool, { taskId: 11, conversationId: 3, toolName: "writer", callId: "c", arguments: {}, replay: "safe" })] }
    // The call's api.commit waits for the core's answer to `end` (the commit's job releasing), as Pi's commit promise does.
    if (ev.kind === EVENT.tx) return { notices: [txResult(ev.id!, ev.body.op === "begin" ? { ready: true } : { result: null })] }
    return undefined
  }, { registry: registryOf([{ name: "x", tools: [tool] }]), models: fauxModels().models })
  r.host.send(EVENT.open)
  await until(() => r.core.ofKind(EVENT.tool_done).length === 1, "no tool_done")
  const tx = r.core.ofKind(EVENT.tx).map(e => e.body)
  assert.equal(tx[0].invocation, 11, "the begin names the call's task")
  // `end` carries the passes the host held the line (PROTOCOL.md, Transactions): a count, never a task state.
  const { elapsed, ...end } = tx.at(-1)
  assert.deepEqual(end, { op: "end" }, "the callback's value is not a task state")
  assert.ok(Number.isInteger(elapsed) && elapsed >= 0, "end reports the passes the host held the line")
  assert.deepEqual(returned, { entry: 7 })
})
