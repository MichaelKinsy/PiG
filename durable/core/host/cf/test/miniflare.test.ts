// The Durable Object host in workerd (Miniflare): open over DO SQL, effects after commits, alarms, cache drop and cold reopen.
import assert from "node:assert/strict"
import { join } from "node:path"
import { after, test } from "node:test"
import { call, startWorker, type WorkerSpec } from "./mf.ts"
import { probeWasm, root, tempDir } from "./helpers.ts"


const out = tempDir("mf")
const spec: WorkerSpec = { entry: join(root, "test", "worker", "probe-do.ts"), outDir: join(out, "dist"), objects: { PI: "ProbeDO" }, wasm: probeWasm() }
const persist = join(out, "persist")
const started = new Set<{ dispose(): Promise<void> }>()
after(async () => { for (const mf of started) await mf.dispose() })
const start = async () => { const mf = await startWorker(spec, persist); started.add(mf); return mf }
const stop = async (mf: { dispose(): Promise<void> }) => { started.delete(mf); await mf.dispose() }

test("a cold open creates Pi's rows; a submit commits before its effects and arms one durable timer and the watchdog as one alarm", async () => {
  const mf = await start()
  const wake = await call<{ coldOpens: number }>(mf, "/wake", {})
  assert.equal(wake.coldOpens, 1)
  const sub = await call<{ status: number; durable: number; volatile: number; alarmWrites: number; alarmAt: number }>(mf, "/submit", { content: "hello", requestId: "r1" })
  assert.equal(sub.status, 0)
  assert.deepEqual(await call(mf, "/rows"), ["1:submit:r1", "2:placed:r1"])
  assert.equal(sub.durable, 1, "the probe's 100 ms timer is durable")
  assert.equal(sub.volatile, 0, "a durable timer is never a setTimeout")
  // The timer (now + 100) then the watchdog (now + 1000): the second does not move the alarm, so the alarm is written once.
  assert.equal(sub.alarmWrites, 1)
  assert.ok(sub.alarmAt > 0)
})

test("the alarm fires the due durable timer, and the core sees it as a timer event", async () => {
  const mf = await start()
  await call(mf, "/wake", {})
  await call(mf, "/drain", {})
  const sub = await call<{ alarmAt: number }>(mf, "/submit", { content: "again", requestId: "r2" })
  assert.ok(sub.alarmAt > 0)
  let notices: string[] = []
  for (let i = 0; i < 100 && !notices.some(n => n.includes('"timer":1')); i++) { await new Promise(r => setTimeout(r, 50)); notices = notices.concat(await call<string[]>(mf, "/drain", {})) }
  assert.ok(notices.some(n => n.includes('"timer":1')), `the alarm never delivered the timer event: ${notices}`)
  const state = await call<{ durable: number; alarmWrites: number }>(mf, "/state")
  assert.equal(state.durable, 0)
  assert.equal(state.alarmWrites, 2, "armed once for the timer, then re-armed once for the watchdog after the timer fired")
})

test("hibernation is a cache drop: after unload and after a runtime restart the object reopens cold and reads the same rows", async () => {
  const mf = await start()
  assert.equal((await call<{ coldOpens: number }>(mf, "/wake", {})).coldOpens, 1)
  const before = await call<string[]>(mf, "/rows")
  assert.ok(before.length >= 4, `rows from the earlier tests are in the store: ${before}`)
  await call(mf, "/unload", {})
  const wake = await call<{ coldOpens: number }>(mf, "/wake", {})
  assert.equal(wake.coldOpens, 2, "one more cold open after the cache drop")
  assert.deepEqual(await call(mf, "/rows"), before)
  await stop(mf)
  const again = await start()
  const wake2 = await call<{ coldOpens: number; notices: string[] }>(again, "/wake", {})
  assert.equal(wake2.coldOpens, 1)
  assert.match(wake2.notices.join(" "), /"created":false/)
  assert.deepEqual(await call(again, "/rows"), before)
})
