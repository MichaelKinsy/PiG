// A caller's context abort reaches the core as `cancel {request}` (PROTOCOL.md, cancellable operations): Pi checks it when the
// operation's line job starts, and a job that started ignores it.
import assert from "node:assert/strict"
import { test } from "node:test"
import { EVENT } from "../src/abi.ts"
import { HostRegistry } from "../src/registry.ts"
import type { Notice } from "../src/wire.ts"
import { apiResult, apis, rig, type ScriptedCore } from "./scripted.ts"

const registry = new HostRegistry({
  snapshot: () => ({ installed: () => [], task: () => undefined, tasks: () => [] }),
  subscribe: () => () => {},
})
const cancels = (core: ScriptedCore) => apis(core).filter(e => e.body?.op === "cancel").map(e => e.body.request)

/** A script that answers nothing, until the test names the notices a `flush` api request delivers. */
function open() {
  const queued: Notice[] = []
  const r = rig(ev => ev.kind === EVENT.api && ev.body?.op === "flush" ? { notices: queued.splice(0) } : undefined, { registry, models: {} as never })
  return { ...r, deliver: (...notices: Notice[]) => { queued.push(...notices); void r.client.api("flush").catch(() => {}) } }
}

test("a submit under a signal is cancellable, and the abort of the signal sends cancel for it until the answer", async () => {
  const r = open()
  const controller = new AbortController()
  const pending = r.client.submit({ conversationId: 1, type: "input" }, undefined, controller.signal)
  pending.catch(() => {})
  const sent = r.core.ofKind(EVENT.submit)
  assert.equal(sent.length, 1)
  assert.equal(sent[0]!.body.cancellable, true)
  assert.deepEqual(cancels(r.core), [])
  controller.abort(new Error("stop"))
  assert.deepEqual(cancels(r.core), [sent[0]!.body.id])
  // The core's rejection of the cancelled request is the signal's reason.
  r.deliver(apiResult(sent[0]!.body.id, { error: { name: "AbortError", message: "This operation was aborted" } }))
  await assert.rejects(pending, /stop/)
})

test("a signal that aborted before the call cancels the request at once, and an answered request is not cancelled", async () => {
  const r = open()
  const aborted = AbortSignal.abort(new Error("early"))
  void r.client.submit({ conversationId: 1, type: "input" }, undefined, aborted).catch(() => {})
  const first = r.core.ofKind(EVENT.submit)[0]!
  assert.deepEqual(cancels(r.core), [first.body.id])
  const controller = new AbortController()
  const pending = r.client.abort({ taskId: 3 }, undefined, controller.signal)
  const second = r.core.ofKind(EVENT.abort)[0]!
  assert.equal(second.body.cancellable, true)
  r.deliver(apiResult(second.body.requestId, { ok: "marked" }))
  assert.equal(await pending, "marked")
  controller.abort()
  assert.deepEqual(cancels(r.core), [first.body.id], "a request that was answered ignores the abort")
})

test("a request of an invocation-bound handle is not cancellable: the core checks the invocation's signal", () => {
  const r = open()
  const controller = new AbortController()
  void r.client.submit({ conversationId: 1, type: "input", invocation: 5 }, undefined, controller.signal).catch(() => {})
  assert.equal(r.core.ofKind(EVENT.submit)[0]!.body.cancellable, undefined)
  controller.abort()
  assert.deepEqual(cancels(r.core), [])
})
