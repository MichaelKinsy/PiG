// Instance-per-isolate session handles and recycling at the high-water mark (ADR D10).
import assert from "node:assert/strict"
import { test } from "node:test"
import { CorePool, type Tenant } from "../src/pool.ts"
import { probeModule } from "./helpers.ts"

type T = Tenant & { log: string[]; busy: boolean }
const tenant = (name: string, busy = false): T => {
  const t: T = { log: [], busy, idle: () => !t.busy, unload: () => { t.log.push("unload") }, broken: e => { t.log.push(`broken ${e.message}`) } }
  void name
  return t
}

test("objects of one isolate share one instance, each with its own handle", () => {
  const pool = new CorePool(probeModule())
  const a = pool.acquire(tenant("a")), b = pool.acquire(tenant("b"))
  assert.equal(pool.created, 1)
  assert.equal(a.core, b.core)
  assert.notEqual(a.session.handle, b.session.handle)
  a.release(); b.release()
})

test("shared: false gives every object its own instance and drops it on release", () => {
  const pool = new CorePool(probeModule(), { shared: false })
  const a = pool.acquire(tenant("a")), b = pool.acquire(tenant("b"))
  assert.equal(pool.created, 2)
  assert.notEqual(a.core, b.core)
  a.release()
  assert.equal(pool.instances, 1)
  b.release()
  assert.equal(pool.instances, 0)
})

test("over the high-water mark: new objects go to a fresh instance, idle tenants of the old one unload, and the old instance is dropped with its last session", () => {
  const pool = new CorePool(probeModule(), { highWater: 1 })
  const idle = tenant("idle"), busy = tenant("busy", true)
  const a = pool.acquire(idle), b = pool.acquire(busy)
  assert.equal(pool.created, 2, "every acquire sees the instance above the mark")
  assert.deepEqual(idle.log, ["unload"], "an idle tenant is asked to drop its cache")
  assert.deepEqual(busy.log, [], "a busy tenant keeps its session")
  a.release()
  assert.equal(pool.instances, 1, "the retired instance went with its last session")
  b.release()
  assert.equal(pool.instances, 0)
  assert.equal(pool.recycled, 2)
})

test("an instance that never exceeds the mark outlives its sessions and serves the next object", () => {
  const pool = new CorePool(probeModule(), { highWater: 1 << 30 })
  const a = pool.acquire(tenant("a"))
  const core = a.core
  a.release()
  const b = pool.acquire(tenant("b"))
  assert.equal(b.core, core)
  assert.equal(pool.created, 1)
  b.release()
})

test("a trap breaks the instance for every tenant on it and the next acquire builds a new one", () => {
  const pool = new CorePool(probeModule())
  const x = tenant("x"), y = tenant("y")
  const a = pool.acquire(x), b = pool.acquire(y)
  a.broke(new Error("unreachable executed"))
  assert.deepEqual([x.log, y.log], [["broken unreachable executed"], ["broken unreachable executed"]])
  const c = pool.acquire(tenant("c"))
  assert.notEqual(c.core, b.core)
  assert.equal(pool.created, 2)
  b.release(); c.release()
})

test("release is idempotent", () => {
  const pool = new CorePool(probeModule())
  const a = pool.acquire(tenant("a"))
  a.release(); a.release()
  assert.equal(pool.instances, 1)
})
