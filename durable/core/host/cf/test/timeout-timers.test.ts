// TimeoutTimers: the timer driver of a host that stays alive.
import assert from "node:assert/strict"
import { test } from "node:test"
import { TimeoutTimers } from "../src/timers.ts"

const wait = (ms: number) => new Promise(resolve => setTimeout(resolve, ms))

test("a timer fires once at its time, measured on the Harness clock", async () => {
  const fired: number[] = []
  let offset = 0
  const timers = new TimeoutTimers(id => fired.push(id), () => Date.now() + offset)
  offset = -10_000 // the Harness clock is ten seconds behind the process clock: the timer is due at once
  timers.set(1, Date.now() - 10_000 + 5, 1)
  await wait(40)
  assert.deepEqual(fired, [1])
  await wait(20)
  assert.deepEqual(fired, [1], "a timer fires once")
  timers.set(9, Date.now() - 10_000 + 80, 1) // 80 ms ahead on the Harness clock
  await wait(30)
  assert.deepEqual(fired, [1], "a timer in the Harness's future waits for it")
  await wait(120)
  assert.deepEqual(fired, [1, 9])
})

test("clear and dispose cancel pending timers, and setting a timer again replaces it", async () => {
  const fired: number[] = []
  const timers = new TimeoutTimers(id => fired.push(id))
  timers.set(1, Date.now() + 15, 0)
  timers.set(2, Date.now() + 15, 0)
  timers.set(3, Date.now() + 15, 0)
  timers.clear(1)
  timers.set(3, Date.now() + 80, 0)
  await wait(40)
  assert.deepEqual(fired, [2], "1 was cleared, 3 was re-armed later")
  timers.dispose()
  await wait(100)
  assert.deepEqual(fired, [2], "dispose cancels what is left")
})
