// The host side of the group-commit ABI (ABI section 5.5): a run of `joined` commits is one transaction, and the `macrotask` effect is one real macrotask.
import assert from "node:assert/strict"
import { test } from "node:test"
import { EFFECT, EVENT } from "../src/abi.ts"
import { FatalError, HostSession } from "../src/host.ts"
import { nodeStore } from "../src/store-node.ts"
import type { Commit } from "../src/wire.ts"
import { ScriptedCore } from "./scripted.ts"

const SQL = ["CREATE TABLE t (a)", "INSERT INTO t VALUES (1)", "INSERT INTO nosuch VALUES (1)"]
const commit = (seq: number, sql: number[], joined: boolean): Commit => ({ seq, joined, stmts: sql.map(id => ({ sql: id, params: [] })) })

function rig(respond: ConstructorParameters<typeof ScriptedCore>[0]) {
  const core = new ScriptedCore(respond)
  const store = nodeStore(":memory:", { wal: false })
  const traced: number[] = []
  const host = new HostSession({ core, sqlTable: SQL, store, clock: () => 1000, trace: { commit: c => traced.push(c.seq) } })
  return { core, host, store, traced }
}

test("a run of joined commits is one transaction; a commit that is not joined starts the next", () => {
  const r = rig(ev => ev.kind === EVENT.resume ? { commits: [commit(1, [0], false), commit(2, [1], true), commit(3, [1], true), commit(4, [1], false)] } : undefined)
  const before = r.store.counters.commits
  r.host.sendJSON(EVENT.resume, {})
  assert.equal(r.store.counters.commits - before, 2, "two transactions for four commits")
  assert.deepEqual(r.traced, [1, 2, 3, 4], "every commit is traced, in order")
  assert.deepEqual(r.store.all("SELECT a FROM t", []), [[1], [1], [1]])
})

test("a joined run that fails applies none of its commits, and fails the handle", () => {
  const r = rig(ev => ev.kind === EVENT.resume ? { commits: [commit(1, [0], false)] } : ev.kind === EVENT.timer ? { commits: [commit(2, [1], false), commit(3, [2], true)] } : undefined)
  r.host.sendJSON(EVENT.resume, {})
  assert.throws(() => r.host.sendJSON(EVENT.timer, new Uint8Array(0)), FatalError)
  assert.deepEqual(r.store.all("SELECT a FROM t", []), [], "the insert of the run rolled back with its failed commit")
})

test("a macrotask effect sends hopped from one macrotask, after the microtasks the step queued", async () => {
  const order: string[] = []
  const r = rig(ev => {
    if (ev.kind === EVENT.resume) return { effects: [{ id: 1, kind: EFFECT.macrotask, payload: new Uint8Array(0) }] }
    if (ev.kind === EVENT.hopped) { order.push("hopped"); return undefined }
    return undefined
  })
  r.host.sendJSON(EVENT.resume, {})
  void Promise.resolve().then(() => order.push("microtask"))
  assert.equal(r.host.inflight, 1)
  assert.deepEqual(order, [])
  await new Promise<void>(resolve => setImmediate(resolve))
  await new Promise<void>(resolve => setImmediate(resolve))
  assert.deepEqual(order, ["microtask", "hopped"])
  assert.equal(r.host.inflight, 0)
})

test("a cancelled macrotask never sends hopped", async () => {
  const r = rig(ev => ev.kind === EVENT.resume ? { effects: [{ id: 1, kind: EFFECT.macrotask, payload: new Uint8Array(0) }, { id: 2, kind: EFFECT.cancel, payload: Uint8Array.of(1, 0, 0, 0) }] } : undefined)
  r.host.sendJSON(EVENT.resume, {})
  await new Promise<void>(resolve => setImmediate(resolve))
  await new Promise<void>(resolve => setImmediate(resolve))
  assert.equal(r.core.ofKind(EVENT.hopped).length, 0)
  assert.equal(r.host.inflight, 0)
})
