// A custom task's phase runs under the definition the core reserved the task under (the effect's handler), even when a
// registry reload has replaced or removed the definition since (scheduler.ts: the phase's task is fixed for the invocation).
import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { customTaskRunner } from "../src/facade/taskruntime.ts"

test("the phase runs the registration the effect names, not the registry's current definition of the kind", async () => {
  const log: string[] = []
  const old = { definition: { name: "k", version: 1, phases: { a: async () => { log.push("old:a") } } } }
  const registry = { task: () => undefined, at: (index: number) => (index === 5 ? old : undefined) }
  const run = customTaskRunner({ registry, hostApi: { client: {} as never }, models: {}, settings: () => ({}), now: () => 0, report: () => {}, snapshot: () => ({}) } as never)
  const effect = { taskId: 7, kind: "k", phase: "a", record: { id: 7, conversationId: 1 }, invocation: 7, handler: 5 }
  await run(effect as never, BACKGROUND_CONTEXT, 1)
  assert.deepEqual(log, ["old:a"])
  await assert.rejects(run({ ...effect, handler: undefined, taskId: 8 } as never, BACKGROUND_CONTEXT, 2), TypeError, "without a registration the registry's current (here: absent) definition is used")
})

test("the runtime's now() and report() throw once the invocation has ended", async () => {
  let runtime: any
  const reports: unknown[] = []
  const task = { definition: { name: "k", version: 1, phases: { a: async (_t: unknown, r: unknown) => { runtime = r } } } }
  const run = customTaskRunner({ registry: { task: () => task, at: () => task }, hostApi: { client: {} as never }, models: {}, settings: () => ({}), now: () => 42, report: (e: unknown) => reports.push(e), snapshot: () => ({}) } as never)
  await run({ taskId: 7, kind: "k", phase: "a", record: { id: 7, conversationId: 1 }, invocation: 7 } as never, BACKGROUND_CONTEXT, 1)
  runtime.now() // the phase returned, the invocation continues
  run.ended(1)
  assert.throws(() => runtime.now(), /Task 7 invocation has ended/)
  assert.throws(() => runtime.report(new Error("late")), /invocation has ended/)
  assert.deepEqual(reports, [])
})

test("runtime.hooks.each runs this task's handlers of the agent's extensions in order, reports a throw and continues", async () => {
  const calls: string[] = []
  const reports: unknown[] = []
  const agent = { extensions: [
    { hooks: [{ task: "k", handlers: { ping: () => { calls.push("a"); throw new Error("a failed") } } }, { task: "other", handlers: { ping: () => calls.push("other task") } }] },
    { hooks: [{ task: "k", handlers: { ping: () => calls.push("b"), pong: () => calls.push("pong") } }] },
  ] }
  let runtime: any
  const task = { definition: { name: "k", version: 1, phases: { a: async (_t: unknown, r: any) => { runtime = r; await r.hooks.each("ping", (handler: () => unknown) => handler()) } } } }
  const run = customTaskRunner({ registry: { task: () => task, at: () => task }, hostApi: { client: {} as never, agent: async () => agent }, models: {}, settings: () => ({}), now: () => 0, report: (e: unknown) => reports.push(e), snapshot: () => ({}) } as never)
  await run({ taskId: 7, kind: "k", phase: "a", record: { id: 7, conversationId: 1 }, invocation: 7 } as never, BACKGROUND_CONTEXT, 1)
  assert.deepEqual(calls, ["a", "b"])
  assert.equal((reports[0] as Error).message, "a failed")
  assert.equal(reports.length, 1)
  void runtime
})

test("one runtime serves every phase of an invocation until the core cancels its latest phase effect", async () => {
  const seen = new Set<unknown>()
  const task = { definition: { name: "k", version: 1, phases: { a: async (_t: unknown, r: unknown) => { seen.add(r) } } } }
  const run = customTaskRunner({ registry: { task: () => task, at: () => task }, hostApi: { client: {} as never }, models: {}, settings: () => ({}), now: () => 0, report: () => {}, snapshot: () => ({}) } as never)
  const effect = { taskId: 7, kind: "k", phase: "a", record: { id: 7, conversationId: 1 }, invocation: 7 }
  await run(effect as never, BACKGROUND_CONTEXT, 1)
  await run(effect as never, BACKGROUND_CONTEXT, 2)
  await run(effect as never, BACKGROUND_CONTEXT, 3)
  assert.equal(seen.size, 1)
  run.ended(3)
  await run(effect as never, BACKGROUND_CONTEXT, 4)
  assert.equal(seen.size, 2, "a new invocation of the task gets a new runtime")
})

test("an invocation the core ends while its handler runs ends when the handler settles, with the signal aborted first", async () => {
  let runtime: any
  let release!: () => void
  const held = new Promise<void>(resolve => { release = resolve })
  const task = { definition: { name: "k", version: 1, phases: { a: async (_t: unknown, r: unknown) => { runtime = r; await held } } } }
  const run = customTaskRunner({ registry: { task: () => task, at: () => task }, hostApi: { client: {} as never }, models: {}, settings: () => ({}), now: () => 0, report: () => {}, snapshot: () => ({}) } as never)
  const phase = run({ taskId: 7, kind: "k", phase: "a", record: { id: 7, conversationId: 1 }, invocation: 7 } as never, BACKGROUND_CONTEXT, 1)
  run.ended(1)
  assert.equal(runtime.signal.aborted, true)
  runtime.now() // still the invocation's handler: the end waits for it
  release()
  await phase
  assert.throws(() => runtime.now(), /invocation has ended/)
})
