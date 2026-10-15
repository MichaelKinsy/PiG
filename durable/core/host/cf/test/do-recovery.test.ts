// Trap recovery of a Durable Object's session (plan 2 item 3, ADR D16): a trap poisons the instance and nothing of the failed step is
// applied. The session drops the instance, reopens a new one from SQLite and feeds a client request that did not complete once; a
// second trap on it fails the request and sets a retry alarm. The core is the real one, opened in storage mode over a fake
// Durable Object storage on node:sqlite.
import assert from "node:assert/strict"
import { existsSync, readFileSync } from "node:fs"
import { DatabaseSync } from "node:sqlite"
import { join } from "node:path"
import { test } from "node:test"
import { EVENT } from "../src/abi.ts"
import { CoreClient } from "../src/client.ts"
import { DoSession } from "../src/do-session.ts"
import { CorePool } from "../src/pool.ts"
import { HostRegistry } from "../src/registry.ts"
import { CoreTrap, type WasmCore, type WasmSession } from "../src/wasm.ts"
import { root, toolchain } from "./helpers.ts"

const wasm = join(root, "dist", `core-${toolchain}.wasm`)

function fakeStorage() {
  const db = new DatabaseSync(":memory:")
  let alarm: number | null = null
  const bind = (params: unknown[]) => params.map(p => p instanceof ArrayBuffer ? new Uint8Array(p) : p) as never[]
  const storage = {
    sql: {
      exec(query: string, ...params: unknown[]) {
        const stmt = db.prepare(query)
        stmt.setReturnArrays(true)
        const rows = stmt.columns().length > 0 ? stmt.all(...bind(params)) as unknown as unknown[][] : (stmt.run(...bind(params)), [])
        return { toArray: () => rows, raw: () => rows[Symbol.iterator](), rowsRead: rows.length, rowsWritten: 0 }
      },
    },
    transactionSync(work: () => void) {
      db.exec("BEGIN")
      try { work(); db.exec("COMMIT") } catch (error) { db.exec("ROLLBACK"); throw error }
    },
    getAlarm: async () => alarm,
    setAlarm: async (at: number) => { alarm = at },
    deleteAlarm: async () => { alarm = null },
  }
  return { storage: storage as never, alarm: () => alarm }
}

/** Arms a trap on the instance a pool creates: its first event of `kind` throws, before or after the core ran it. */
function trapEvery(plan: (instance: number) => { kind: number; when: "before" | "after" } | undefined) {
  let instance = 0
  return (core: WasmCore) => {
    const mine = plan(instance++)
    const original = core.newSession.bind(core)
    core.newSession = () => {
      const session = original()
      return new Proxy(session, {
        get(target, name) {
          const value = (target as never as Record<string | symbol, unknown>)[name]
          if (name !== "step" && name !== "rows") return typeof value === "function" ? value.bind(target) : value
          return (...args: unknown[]) => {
            if (!mine || name !== "step" || args[0] !== mine.kind) return (value as (...a: unknown[]) => unknown).apply(target, args)
            if (mine.when === "after") (value as (...a: unknown[]) => unknown).apply(target, args)
            throw new CoreTrap(`injected trap ${mine.when} event ${mine.kind}`)
          }
        },
      }) as WasmSession
    }
  }
}

const OPEN = { mode: "storage", settings: {}, models: [], registry: { extensions: [] }, sidecar: "off", delta: true }
const runtime = () => ({
  registry: new HostRegistry({ snapshot: () => ({ installed: () => [], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} } as never),
  models: { getModel: () => ({}), streamSimple: () => { throw new Error("no models") } } as never,
  report: () => {},
})

const session = (plan: Parameters<typeof trapEvery>[0]) => {
  const module = new (WebAssembly.Module as never as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(wasm))
  const fake = fakeStorage()
  const fatal: Error[] = []
  const pool = new CorePool(module, { onInstance: trapEvery(plan) })
  const s = new DoSession({ pool, storage: fake.storage, open: () => OPEN, runtime, onFatal: e => { fatal.push(e) }, clock: () => 1_000_000 })
  return { s, pool, fake, fatal }
}

const read = (s: DoSession) => s.client.api("storage.task", { id: 1 })

for (const when of ["before", "after"] as const) {
  test(`a trap ${when} a client request reopens a new instance and the request is fed once: the caller sees the answer`, { skip: !existsSync(wasm) }, async () => {
    const { s, pool, fatal } = session(n => n === 0 ? { kind: EVENT.api, when } : undefined)
    s.ensureOpen()
    assert.equal(await read(s), null)
    assert.equal(pool.created, 2, "a new instance replaced the poisoned one")
    assert.equal(pool.instances, 1)
    assert.equal(s.coldOpens, 2)
    assert.deepEqual(fatal, [], "a recovered trap is not a fatal")
    assert.equal(await read(s), null, "the reopened session serves the next call")
    s.close()
  })
}

test("a second trap on the fed request is final: the caller gets the error and a retry alarm is set with backoff", { skip: !existsSync(wasm) }, async () => {
  const { s, fake, fatal } = session(() => ({ kind: EVENT.api, when: "before" }))
  s.ensureOpen()
  await assert.rejects(read(s), /injected trap before event/)
  assert.equal(fatal.length, 1, "reported once")
  assert.ok((fatal[0] as { recent?: unknown[] }).recent?.length, "the report carries the recent events")
  await new Promise(resolve => setTimeout(resolve, 20))
  assert.equal(fake.alarm(), 1_000_000 + 2000, "the second consecutive trap waits 2 s (1 s, 2 s, 4 s ...)")
  assert.equal(s.open, false, "the next call cold-opens")
  s.close()
})

test("a trap on the reopen itself is not retried in place", { skip: !existsSync(wasm) }, async () => {
  const { s, pool, fatal } = session(n => n === 0 ? { kind: EVENT.api, when: "before" } : { kind: EVENT.open, when: "before" })
  s.ensureOpen()
  await assert.rejects(read(s), /injected trap/)
  assert.equal(pool.created, 2)
  assert.ok(fatal.length >= 1)
  s.close()
})

test("baseline: a client request answers without a trap", { skip: !existsSync(wasm) }, async () => {
  const { s } = session(() => undefined)
  s.ensureOpen()
  assert.equal(await read(s), null)
  s.close()
})

test("failAll rejects every in-flight request except the one being fed again", async () => {
  const sent: number[] = []
  const host = { onNotice: () => () => {}, send: (_kind: number, _payload: unknown, request: number) => { sent.push(request); return undefined } }
  const client = new CoreClient(host as never)
  const first = client.api("x"), second = client.api("y"), third = client.api("z")
  assert.deepEqual(sent, [1, 2, 3])
  const outcome = (p: Promise<unknown>) => Promise.race([p.then(() => "answered", (e: Error) => e.message), new Promise(resolve => setImmediate(() => resolve("pending")))])
  client.failAll(new Error("trapped"), 2)
  assert.deepEqual(await Promise.all([outcome(first), outcome(second), outcome(third)]), ["trapped", "pending", "trapped"])
  client.failAll(new Error("again"))
  assert.equal(await outcome(second), "again")
})
