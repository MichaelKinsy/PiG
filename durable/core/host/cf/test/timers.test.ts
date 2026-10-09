// Alarm and timer policy (ADR D7, D8): one alarm at the earliest durable time or the watchdog, written only when the target
// changes; volatile timers are setTimeout; early and duplicate wakes are no-ops.
import assert from "node:assert/strict"
import { test } from "node:test"
import { AlarmTimers } from "../src/timers.ts"

function rig(now = { t: 1000 }) {
  const writes: string[] = []
  const fired: number[] = []
  const timers = new AlarmTimers({ setAlarm: at => { writes.push(`set ${at}`) }, deleteAlarm: () => { writes.push("delete") } }, id => fired.push(id), () => now.t)
  return { timers, writes, fired, now }
}

test("the alarm sits at the earliest of the durable timers and the watchdog and is written only when that time changes", () => {
  const r = rig()
  r.timers.set(1, 5000, true)
  r.timers.liveness(9000)
  r.timers.set(2, 7000, true)
  assert.deepEqual(r.writes, ["set 5000"], "later targets never move the alarm")
  r.timers.clear(1)
  assert.deepEqual(r.writes, ["set 5000", "set 7000"])
  r.timers.liveness(-1)
  r.timers.clear(2)
  assert.deepEqual(r.writes, ["set 5000", "set 7000", "delete"])
  assert.equal(r.timers.alarmWrites, 3)
})

test("a durable timer never creates a setTimeout, so an idle object can hibernate; a volatile one does", async () => {
  const r = rig({ t: Date.now() })
  r.timers.set(1, r.now.t + 60_000, true)
  assert.equal(r.timers.volatilePending, 0)
  r.timers.set(2, r.now.t + 5, false)
  assert.equal(r.timers.volatilePending, 1)
  await new Promise(resolve => setTimeout(resolve, 40))
  assert.deepEqual(r.fired, [2])
  assert.equal(r.timers.volatilePending, 0)
  r.timers.set(3, r.now.t + 60_000, false)
  r.timers.clear(3)
  assert.equal(r.timers.volatilePending, 0, "clear cancels the timeout")
  r.timers.stop()
})

test("an alarm runs only the durable timers that are due; an early or duplicate wake fires nothing and a spent watchdog is dropped", () => {
  const r = rig()
  r.timers.set(1, 2000, true)
  r.timers.set(2, 8000, true)
  r.timers.liveness(3000)
  r.now.t = 1500
  r.timers.due()
  assert.deepEqual(r.fired, [], "early wake")
  r.now.t = 3100
  r.timers.due()
  assert.deepEqual(r.fired, [1])
  assert.equal(r.timers.alarmAt, 8000, "the spent watchdog no longer holds the alarm; the next timer does")
  r.timers.due()
  assert.deepEqual(r.fired, [1], "duplicate wake")
  r.now.t = 9000
  r.timers.due()
  assert.deepEqual(r.fired, [1, 2])
  assert.equal(r.timers.alarmAt, -1)
  assert.equal(r.writes.at(-1), "set 8000", "a fired alarm is already gone: nothing is deleted or written")
})

test("re-setting a timer replaces it: the same ID never fires twice", () => {
  const r = rig()
  r.timers.set(1, 2000, true)
  r.timers.set(1, 4000, true)
  r.now.t = 2500
  r.timers.due()
  assert.deepEqual(r.fired, [])
  r.now.t = 4000
  r.timers.due()
  assert.deepEqual(r.fired, [1])
})

test("a failing alarm write is reported, not swallowed", async () => {
  const errors: unknown[] = []
  const timers = new AlarmTimers({ setAlarm: () => Promise.reject(new Error("quota")), deleteAlarm: () => {} }, () => {}, () => 0, e => errors.push(e))
  timers.set(1, 10, true)
  await new Promise(resolve => setTimeout(resolve, 5))
  assert.equal((errors[0] as Error).message, "quota")
})
