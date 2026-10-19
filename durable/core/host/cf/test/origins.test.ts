// Origins (ABI section 5.4): the host snapshots the async context of each call a client makes, tags the event with it, and runs the
// effects and notices the core stamps with that tag inside the snapshot; it drops a snapshot when the core says it holds no work for it.
import assert from "node:assert/strict"
import { AsyncLocalStorage } from "node:async_hooks"
import { test } from "node:test"
import { EFFECT, EVENT, NOTICE } from "../src/abi.ts"
import { HostSession } from "../src/host.ts"
import { nodeStore } from "../src/store-node.ts"
import { published, ScriptedCore, type Seen, type Spec } from "./scripted.ts"

const als = new AsyncLocalStorage<string>()
const u32s = (...n: number[]) => { const b = new Uint8Array(4 * n.length); const dv = new DataView(b.buffer); n.forEach((v, i) => dv.setUint32(4 * i, v, true)); return b }
const hook = (id: number, origin: number) => ({ id, kind: EFFECT.hook, origin, payload: new TextEncoder().encode("{}") })
const freed = (...origins: number[]) => ({ kind: NOTICE.origin_free, origin: 0, payload: u32s(origins.length, ...origins) })

function rig(respond: (ev: Seen, origin: number) => Spec | undefined) {
  const origins: number[] = []
  const core = new ScriptedCore()
  core.respond = ev => respond(ev, origins[origins.length - 1]!)
  const step = core.step.bind(core)
  core.step = (kind, payload, now, origin = 0) => { origins.push(origin); return step(kind, payload, now, origin) }
  const handled: (string | undefined)[] = []
  const heard: (string | undefined)[] = []
  const listened: number[] = []
  const host = new HostSession({
    core, sqlTable: [], store: nodeStore(":memory:", { wal: false }), clock: () => 1,
    handlers: { [EFFECT.hook]: () => { handled.push(als.getStore()) } },
    onNotice: n => { heard.push(als.getStore()); listened.push(n.kind) },
  })
  return { core, host, origins, handled, heard, listened }
}

test("a host call's async context reaches the effects and notices the core stamps with its origin", () => {
  const r = rig((ev, origin) => (ev.kind === EVENT.submit ? { effects: [hook(1, origin)], notices: [{ kind: NOTICE.published, origin, payload: new TextEncoder().encode("{}") }] } : undefined))
  als.run("submit", () => r.host.send(EVENT.submit, new TextEncoder().encode("{}")))
  assert.notEqual(r.origins[0], 0, "a call a client makes is tagged")
  assert.deepEqual(r.handled, ["submit"], "the effect's handler runs in the call's context")
  assert.deepEqual(r.heard, ["submit"], "the notice's listeners run in it too")
})

test("the origin of a step's effects is the core's to choose: a later call's context does not leak into them", () => {
  let first = 0
  const r = rig((ev, origin) => {
    if (ev.kind === EVENT.submit) { first = origin; return undefined }
    // A later call asks for work the core started for the first one.
    return ev.kind === EVENT.inspect ? { effects: [hook(1, first)] } : undefined
  })
  als.run("first", () => r.host.send(EVENT.submit, new TextEncoder().encode("{}")))
  als.run("later", () => r.host.send(EVENT.inspect, new TextEncoder().encode("{}")))
  assert.deepEqual(r.handled, ["first"])
})

test("the completion of an effect is sent with no origin: the core continues the effect's own", () => {
  const r = rig(() => undefined)
  als.run("completion", () => r.host.hookDone(1, 0, null))
  assert.deepEqual(r.origins, [0])
})

test("an origin_free notice drops the snapshot, reaches no listener, and the core must not name the origin again", () => {
  let origin = 0
  const r = rig((ev, o) => {
    if (ev.kind === EVENT.submit) { origin = o; return { notices: [published({}), freed(o)] } }
    return ev.kind === EVENT.inspect ? { effects: [hook(1, origin)] } : undefined
  })
  r.host.send(EVENT.submit, new TextEncoder().encode("{}"))
  assert.deepEqual(r.listened, [NOTICE.published], "the free notice is the host's own")
  assert.throws(() => r.host.send(EVENT.inspect, new TextEncoder().encode("{}")), /origin .* the host does not hold/)
  assert.ok(r.host.discarded)
})

test("a step that is not ok leaves nothing of its event in the core: the host drops its origin", () => {
  let origin = 0
  const r = rig((ev, o) => {
    if (ev.kind === EVENT.submit) { origin = o; return { status: 1, error: { name: "Error", message: "no" } } }
    return ev.kind === EVENT.inspect ? { effects: [hook(1, origin)] } : undefined
  })
  r.host.send(EVENT.submit, new TextEncoder().encode("{}"))
  assert.throws(() => r.host.send(EVENT.inspect, new TextEncoder().encode("{}")), /does not hold/)
})
