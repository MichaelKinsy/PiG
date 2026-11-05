// HarnessOptions.setTimer (packages/durable/src/timers.ts hostTimers): a foreground timer the host declines or throws on is armed
// with setTimeout and reported once; a background timer the host declines is dropped; the driver disarms what it armed.
import assert from "node:assert/strict"
import { test } from "node:test"
import { hostTimers, SetTimerTimers, type SetTimer } from "../src/timers.ts"

test("a foreground timer the host declines is armed with setTimeout, and the first such failure is reported once", async () => {
  const reports: string[] = []
  const timers = hostTimers(() => undefined, error => reports.push((error as Error).message))
  let fired = 0
  timers.foreground(() => { fired++ }, 1)
  timers.foreground(() => { fired++ }, 1)
  await new Promise(resolve => setTimeout(resolve, 20))
  assert.equal(fired, 2)
  assert.deepEqual(reports, ["HarnessOptions.setTimer declined a timer that paces work; using setTimeout"])
})

test("a background timer the host throws on is reported and declined", () => {
  const reports: unknown[] = []
  const boom = new Error("boom")
  const timers = hostTimers(() => { throw boom }, error => reports.push(error))
  assert.equal(timers.background(() => {}, 1), undefined)
  assert.deepEqual(reports, [boom])
})

test("the driver arms volatile and durable timers as foreground and background ones as background, and disarms them", () => {
  const armed: { delayMs: number; background: boolean; disarmed: boolean; callback: () => void }[] = []
  const setTimer: SetTimer = (callback, delayMs, options) => {
    if (options.background) return undefined
    const timer = { delayMs, background: options.background, disarmed: false, callback }
    armed.push(timer)
    return () => { timer.disarmed = true }
  }
  const fired: number[] = []
  const driver = new SetTimerTimers(hostTimers(setTimer, () => {}), id => fired.push(id), () => 1000)
  driver.set(1, 1500, 1)
  driver.set(2, 1200, 0)
  driver.set(3, 9000, 2)
  assert.deepEqual(armed.map(t => t.delayMs), [500, 200])
  armed[0]!.callback()
  assert.deepEqual(fired, [1])
  driver.clear(2)
  assert.equal(armed[1]!.disarmed, true)
  driver.set(4, 2000, 1)
  driver.dispose()
  assert.equal(armed[2]!.disarmed, true)
})
