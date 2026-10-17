// One Wasm instance per object and recycling at the high-water mark (plan 2 item 3).
import assert from "node:assert/strict"
import { test } from "node:test"
import { CorePool, type Tenant } from "../src/pool.ts"
import { probeModule } from "./helpers.ts"

type T = Tenant & { log: string[]; busy: boolean }
const tenant = (busy = false): T => {
  const t: T = { log: [], busy, idle: () => !t.busy, unload: () => { t.log.push("unload") }, broken: e => { t.log.push(`broken ${e.message}`) } }
  return t
}

test("every object has its own instance and the instance is dropped with its session", () => {
  const pool = new CorePool(probeModule())
  const a = pool.acquire(tenant()), b = pool.acquire(tenant())
  assert.equal(pool.created, 2)
  assert.notEqual(a.core, b.core)
  a.release()
  assert.equal(pool.instances, 1)
  b.release()
  assert.equal(pool.instances, 0)
  assert.equal(pool.recycled, 2)
})

test("over the high-water mark an idle object is asked to drop its instance; a busy one keeps it", () => {
  const pool = new CorePool(probeModule(), { highWater: 1 })
  const idle = tenant(), busy = tenant(true)
  const a = pool.acquire(idle), b = pool.acquire(busy)
  a.idled(); b.idled()
  assert.deepEqual(idle.log, ["unload"])
  assert.deepEqual(busy.log, [])
  a.release(); b.release()
})

test("under the mark idling keeps the instance", () => {
  const pool = new CorePool(probeModule(), { highWater: 1 << 30 })
  const t = tenant()
  const a = pool.acquire(t)
  a.idled()
  assert.deepEqual(t.log, [])
  a.release()
})

test("a trap breaks only the trapped object's instance; the next acquire builds a new one", () => {
  const pool = new CorePool(probeModule())
  const x = tenant(), y = tenant()
  const a = pool.acquire(x), b = pool.acquire(y)
  a.broke(new Error("unreachable executed"))
  assert.deepEqual([x.log, y.log], [["broken unreachable executed"], []])
  assert.equal(pool.instances, 1)
  const c = pool.acquire(tenant())
  assert.notEqual(c.core, a.core)
  assert.notEqual(c.core, b.core)
  assert.equal(pool.created, 3)
  b.release(); c.release()
})

test("release is idempotent", () => {
  const pool = new CorePool(probeModule())
  const a = pool.acquire(tenant())
  a.release(); a.release()
  assert.equal(pool.instances, 0)
})
