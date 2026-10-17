// The context read an invocation gets: Pi's options object ({at}) and immutable records (context.ts kept ranges).
import assert from "node:assert/strict"
import { test } from "node:test"
import type { Context } from "@earendil-works/chord"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { invocationApi } from "../src/facade/hostapi.ts"

const view = () => ({ head: { id: 1, m: { a: 1 } }, entries: [{ id: 1, model: [{ role: "user", content: "x" }] }], contributions: [[{ role: "user", content: "x" }]], messages: [{ role: "user", content: "x" }] })

test("context sends {at} from the options object and hands the records over frozen, deeply, with mutable arrays", async () => {
  const sent: Record<string, unknown>[] = []
  const client = { api: async (_op: string, args: Record<string, unknown>) => { sent.push(args); return view() } } as never
  const api = invocationApi({ client }, { taskId: 7, conversationId: 3 })
  const result: any = await api.context(3, BACKGROUND_CONTEXT, { at: 9 })
  assert.deepEqual(sent[0], { taskId: 7, conversationId: 3, at: 9 }, "the invocation's task names the runtime whose context this is (ctxread.go runtimeContext)")
  await api.context(3, BACKGROUND_CONTEXT)
  assert.deepEqual(sent[1], { taskId: 7, conversationId: 3 }, "no cutoff: the newest context")
  assert.throws(() => { result.entries[0].model[0].content = "y" }, TypeError)
  assert.throws(() => { result.contributions[0][0].content = "y" }, TypeError)
  assert.throws(() => { result.messages[0].content = "y" }, TypeError)
  assert.throws(() => { result.head.m.a = 2 }, TypeError)
  result.messages.length = 0
  result.entries.pop()
  assert.equal(result.messages.length + result.entries.length, 0, "the arrays belong to the caller")
})

import { invocationBinding } from "../src/facade/hostapi.ts"

test("an invocation's reads reject as 'invocation has ended' once it ended, including a wait the core cancelled for it", async () => {
  let fail!: (error: Error) => void
  const client = { api: (_op: string) => new Promise((_, reject) => { fail = reject }) } as never
  const { binding, end } = invocationBinding(7, undefined)
  const api = invocationApi({ client }, { taskId: 7, conversationId: 3 }, binding)
  const waiting = api.waitForTask(9, BACKGROUND_CONTEXT)
  end()
  fail(new Error("The operation was aborted"))
  await assert.rejects(waiting, /Task 7 invocation has ended/)
  await assert.rejects(api.getTask(9, BACKGROUND_CONTEXT), /Task 7 invocation has ended/)
})

test("a watch opened through an invocation stops when the invocation ends, and one opened after it rejects", async () => {
  let stops = 0
  const watch = { stop() { stops++ } }
  const { binding, end } = invocationBinding(7, undefined)
  const api = invocationApi({ client: {} as never, watchDoc: async () => watch }, { taskId: 7, conversationId: 3 }, binding)
  assert.equal(await api.watchDoc({} as never, 3, BACKGROUND_CONTEXT), watch)
  assert.equal(stops, 0)
  end()
  assert.equal(stops, 1)
  await assert.rejects(api.watchDoc({} as never, 3, BACKGROUND_CONTEXT), /invocation has ended/)
})

import { invocationCommit } from "../src/facade/hostapi.ts"

test("an ended invocation's snapshot and commit reject as 'invocation has ended', also when the core refused the commit for it", async () => {
  const client = { api: async () => ({ text: "two" }), begin: async () => { throw new Error("transaction begin: no running invocation of task 7") } } as never
  const { binding, end } = invocationBinding(7, undefined)
  const api = invocationApi({ client }, { taskId: 7, conversationId: 3 }, binding)
  const commit = invocationCommit({ client }, { invocation: 7, conversationId: 3 }, binding)
  const doc = { definition: { kind: "k", version: 1, scope: "session" } } as never
  assert.deepEqual(await api.snapshot(doc, BACKGROUND_CONTEXT), { text: "two" })
  await assert.rejects(commit(() => undefined, BACKGROUND_CONTEXT), /no running invocation/, "while alive the core's own error shows")
  end()
  await assert.rejects(api.snapshot(doc, BACKGROUND_CONTEXT), /Task 7 invocation has ended/)
  await assert.rejects(commit(() => undefined, BACKGROUND_CONTEXT), /Task 7 invocation has ended/)
})

test("agent() resolves with the invocation's context: a caller that cancelled does not fail the phase's resolution", async () => {
  let resolvedWith: Context | undefined
  const { binding } = invocationBinding(7, new AbortController().signal)
  const api = invocationApi({ client: {} as never, agent: async (_id: number, context: Context) => { resolvedWith = context; return { thinkingLevel: "off" } } }, { taskId: 7, conversationId: 3 }, binding)
  const caller = new AbortController(); caller.abort(new Error("caller gone"))
  await assert.rejects(api.agent({ ...BACKGROUND_CONTEXT, abortSignal: caller.signal }), /caller gone/)
  assert.deepEqual(await api.agent(BACKGROUND_CONTEXT), { thinkingLevel: "off" })
  assert.equal(resolvedWith?.abortSignal, binding.signal, "bound to the invocation's signal, not the first caller's")
})
