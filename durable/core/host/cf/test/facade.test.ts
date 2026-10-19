// The pi-durable API facade against a scripted core: which events, transactions and requests each public call produces.
import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { EFFECT, EVENT, NOTICE } from "../src/abi.ts"
import { AgentDoc, createRegistry, defineDoc, defineExtension, defineTool, ConversationBusy } from "../src/facade/index.ts"
import { MemoryStorage } from "../src/facade/storage.ts"
import { Harness } from "../src/facade/harness.ts"
import { FatalError } from "../src/host.ts"
import { apiResult, published, ScriptedCore, txResult, type Seen } from "./scripted.ts"

const context = BACKGROUND_CONTEXT
const models = () => { const faux = fauxProvider(); const m = createModels(); m.setProvider(faux.provider); return m as any }

/** A core that holds the line the way the protocol says: begin answers ready, each operation answers its result. */
function line(handler: (ev: Seen, txOps: { op: string; [k: string]: any }[]) => { result?: unknown }) {
  const log: { op: string; [k: string]: any }[] = []
  const core = new ScriptedCore(ev => {
    if (ev.kind === EVENT.tx) {
      const op = ev.body
      if (op.op === "begin") return { notices: [txResult(ev.id!, { ready: true })] }
      if (op.op === "end") { log.push(op); return { notices: [txResult(ev.id!, { result: null })] } }
      if (op.op === "abort") { log.push(op); return undefined }
      log.push(op)
      const answer = handler(ev, log) as any
      return { notices: [txResult(ev.id!, { result: answer && "result" in answer ? answer.result : null })] }
    }
    return undefined
  })
  return { core, log }
}

const NO_EXT = createRegistry()

async function harness(core: ScriptedCore, extra: Record<string, unknown> = {}) {
  return Harness.open(new MemoryStorage(), { models: models(), registry: NO_EXT, core: { newSession: () => core, sqlTable: [] }, ...extra } as any, context)
}

test("open sends the registry snapshot, settings and sidecar mode in one open event", async () => {
  const tool = defineTool({ name: "read", description: "Read", parameters: { type: "object" }, replay: "safe", execute: async () => ({ output: [] }) })
  const registry = createRegistry()
  registry.install(defineExtension({ name: "files", tools: [tool] }))
  const { core } = line(() => ({}))
  await harness(core, { registry, settings: { progress: { partialIntervalMs: 50 } } })
  const open = core.ofKind(EVENT.open)[0]!.body
  assert.equal(open.mode, "harness")
  assert.equal(open.settings.progress.partialIntervalMs, 50)
  assert.equal(open.settings.retry.maxRetries, 3, "settings are resolved over the defaults")
  assert.deepEqual(open.registry.extensions.map((e: any) => [e.name, e.tools.map((t: any) => [t.name, t.replay])]), [["files", [["read", "safe"]]]])
  assert.equal(open.sidecar, "index")
})

test("open names every model of the models object as {provider, id, contextWindow, maxTokens}; modelsInUse replaces the list", async () => {
  const { core } = line(() => ({}))
  await harness(core)
  const models = core.ofKind(EVENT.open)[0]!.body.models
  assert.ok(models.length > 0, "the core can only run a model the open event names")
  assert.deepEqual(Object.keys(models[0]).sort(), ["contextWindow", "id", "maxTokens", "provider"])
  const { core: other } = line(() => ({}))
  await harness(other, { modelsInUse: () => [{ provider: "p", id: "m", contextWindow: 1 }] })
  assert.deepEqual(other.ofKind(EVENT.open)[0]!.body.models, [{ provider: "p", id: "m", contextWindow: 1 }])
})

test("root() creates the root, its agent and init in one commit: one begin, the ops in order, one end", async () => {
  const Notes = defineDoc({ kind: "example.notes", version: 1, scope: "conversation", history: "rewindable", fork: "asOf", initial: () => ({ text: "" }) })
  const { core, log } = line(ev => {
    const op = ev.body.op
    if (op === "conversation") return { result: null }
    if (op === "createRootConversation") return { result: { id: 1 } }
    if (op === "doc") return { result: { value: ev.body.address.definition === "pi.agent" ? {} : { text: "" } } }
    return { result: null }
  })
  const h = await harness(core)
  const root = await h.root(context, { agent: { thinkingLevel: "low" }, init: async (tx: any, id: number) => { (await tx.doc(Notes, id)).text = "root notes" } })
  assert.equal(root.id, 1)
  assert.deepEqual(log.map(o => o.op), ["conversation", "createRootConversation", "doc", "doc", "doc.write", "doc.write", "end"])
  const writes = log.filter(o => o.op === "doc.write")
  assert.deepEqual(writes.map(w => [w.address.definition, w.ops]), [["pi.agent", [["s", ["thinkingLevel"], "low"]]], ["example.notes", [["a", ["text"], "root notes"]]]])
  assert.equal(core.ofKind(EVENT.tx).filter(e => e.body.op === "begin").length, 1)
})

test("a commit that throws aborts the transaction and the error reaches the caller; nothing is written", async () => {
  const { core, log } = line(() => ({}))
  const h = await harness(core)
  await assert.rejects(h.commit(async () => { throw new RangeError("nope") }, context), RangeError)
  assert.deepEqual(log.map(o => o.op), ["abort"])
  assert.equal(log[0]!.error.name, "RangeError")
})

test("submit sends one submit event and resolves when the core answers it; wait settles on the terminal notice", async () => {
  let sent: any
  const core = new ScriptedCore(ev => {
    if (ev.kind === EVENT.submit) { sent = ev.body; return { notices: [published({ submissions: [{ id: 9, requestId: ev.body.requestId, status: "placed" }] }), apiResult(ev.body.id, { ok: { id: 9 } })] } }
    if (ev.kind === EVENT.registry) return undefined
    return undefined
  })
  const h = await harness(core)
  const conversation = (await (async () => { core.respond = ev => ev.kind === EVENT.api ? { notices: [apiResult(ev.id!, { ok: { id: 1 } })] } : undefined; return h.conversation(1, context) })())!
  core.respond = ev => {
    if (ev.kind === EVENT.submit) { sent = ev.body; return { notices: [published({ submissions: [{ id: 9, requestId: ev.body.requestId, status: "placed" }] }), apiResult(ev.body.id, { ok: { id: 9 } })] } }
    return undefined
  }
  const submission = await conversation.submit({ type: "input", content: "hi" }, context)
  assert.equal(submission.id, 9)
  assert.equal(sent.conversationId, 1)
  assert.equal(sent.type, "input")
  assert.equal(typeof sent.id, "number", "the event names the api_result key the core answers on")
  assert.equal(sent.requestId, undefined, "a request ID reaches the record only when the caller gave one (pi-durable)")
  await conversation.submit({ type: "input", content: "again", requestId: "r-1" }, context)
  assert.equal(sent.requestId, "r-1", "a caller's request ID is sent as given")
  assert.equal(core.ofKind(EVENT.resume).length, 2, "every submit kicks the scheduler (scheduler.ts resume() :267-270 is idempotent but each call sets dirty and queues a drain)")
  // The settled record is read from Pi's table by the host; the wait ends on the published notice for this submission.
  ;(h as any).store.run("CREATE TABLE IF NOT EXISTS submissions (id INTEGER PRIMARY KEY, conversation_id INTEGER, request_id TEXT, status TEXT, record TEXT)", [])
  core.respond = ev => ev.kind === EVENT.api && ev.body.op === "submission" ? { notices: [apiResult(ev.id!, { ok: { id: 9, status: "placed" } })] } : undefined
  const waiting = submission.wait(context)
  await new Promise(r => setTimeout(r, 10))
  ;(h as any).store.run("INSERT INTO submissions VALUES (9, 1, 'x', 'done', ?)", [JSON.stringify({ id: 9, status: "done" })])
  ;(h as any).onNotice(1, new TextEncoder().encode(JSON.stringify({ submissions: [{ id: 9, requestId: "x", status: "done" }] })))
  assert.deepEqual(await waiting, { id: 9, status: "done" })
})

test("a fatal core failure rejects a pending wait with its cause instead of leaving it unsettled", async () => {
  const core = new ScriptedCore(ev => {
    if (ev.kind === EVENT.submit) return { notices: [published({ submissions: [{ id: 9, requestId: ev.body.requestId, status: "placed" }] }), apiResult(ev.body.id, { ok: { id: 9 } })] }
    return undefined
  })
  const reports: unknown[] = []
  const h = await harness(core, { onReport: (e: unknown) => reports.push(e) })
  const conversation = new (await import("../src/facade/harness.ts")).Conversation(h, 1)
  const submission = await conversation.submit({ type: "input", content: "hi" }, context)
  ;(h as any).store.run("CREATE TABLE IF NOT EXISTS submissions (id INTEGER PRIMARY KEY, conversation_id INTEGER, request_id TEXT, status TEXT, record TEXT)", [])
  core.respond = ev => ev.kind === EVENT.api && ev.body.op === "submission" ? { notices: [apiResult(ev.id!, { ok: { id: 9, status: "placed" } })] } : undefined
  const waiting = submission.wait(context)
  await new Promise(r => setTimeout(r, 10))
  // A trapped core (out of memory, unreachable) discards the session: the host's onFatal.
  ;(h as any).host.discard(new FatalError("the core failed on a 8 event: the core trapped: unreachable"))
  await assert.rejects(waiting, /the core trapped: unreachable/)
  await assert.rejects(submission.wait(context), /the core trapped/, "a later wait rejects at once")
  assert.equal(reports.length, 1, "the failure is reported once")
})

test("a rejected submit is Pi's error: ConversationBusy", async () => {
  const core = new ScriptedCore(ev => ev.kind === EVENT.submit ? { notices: [apiResult(ev.body.id, { error: { name: "ConversationBusy", message: "Conversation 1 is busy" } })] } : undefined)
  const h = await harness(core)
  const c = new (await import("../src/facade/harness.ts")).Conversation(h, 1)
  await assert.rejects(c.submit({ type: "input", content: "x", whenBusy: "reject" }, context), (e: Error) => e instanceof ConversationBusy && (e as ConversationBusy).conversationId === 1, "Pi's ConversationBusy carries the conversation ID as a number")
})

test("agent() resolves the stored agent state against the live registry in the host, wraps included", async () => {
  const keep = defineTool({ name: "keep", description: "k", parameters: {}, execute: async () => ({ output: [] }) })
  const drop = defineTool({ name: "drop", description: "d", parameters: {}, execute: async () => ({ output: [] }) })
  const registry = createRegistry()
  registry.install(defineExtension({ name: "a", tools: [keep, drop] }))
  const core = new ScriptedCore(ev => ev.kind === EVENT.api && ev.body.op === "snapshot" ? { notices: [apiResult(ev.id!, { ok: { tools: { remove: ["drop"] }, thinkingLevel: "low" } })] } : undefined)
  const h = await harness(core, { registry })
  const agent: any = await h.resolveAgent(1, undefined, context)
  assert.deepEqual(agent.tools.map((t: any) => t.name), ["keep"])
  assert.equal(agent.thinkingLevel, "low")
  assert.equal(core.ofKind(EVENT.api).find(e => e.body.op === "snapshot")!.body.doc.definition, "pi.agent")
  void AgentDoc
})

test("close seals the core session and rejects later use", async () => {
  const { core } = line(() => ({}))
  const h = await harness(core)
  await h.close(context)
  assert.equal(core.ofKind(EVENT.close).length, 1)
  assert.ok(core.freed)
  await assert.rejects(h.commit(async () => 1, context), /closed/)
})

test("a registry publication after open is sent to the core as a registry event", async () => {
  const registry = createRegistry()
  const { core } = line(() => ({}))
  await harness(core, { registry })
  registry.install(defineExtension({ name: "late", tools: [] }))
  const events = core.ofKind(EVENT.registry)
  assert.equal(events.length, 1)
  assert.deepEqual(events[0]!.body.registry.extensions.map((e: any) => e.name), ["late"])
})

test("settings are live: a change to the caller's settings object reaches the core before the next event, once", async () => {
  const settings: Record<string, unknown> = { followUpMode: "one" }
  const core = new ScriptedCore(ev => ev.kind === EVENT.api ? { notices: [apiResult(ev.id!, { ok: null })] } : undefined)
  const h = await harness(core, { settings })
  const before = core.ofKind(EVENT.registry).length
  await h.snapshot(AgentDoc, 1, context)
  assert.equal(core.ofKind(EVENT.registry).length, before, "unchanged settings send nothing")
  settings.followUpMode = "all"
  await h.snapshot(AgentDoc, 1, context)
  await h.snapshot(AgentDoc, 1, context)
  const sent = core.ofKind(EVENT.registry).slice(before)
  assert.equal(sent.length, 1)
  assert.equal(sent[0]!.body.settings.followUpMode, "all")
  const kinds = core.seen.map(e => e.kind)
  assert.ok(kinds.lastIndexOf(EVENT.registry) < kinds.indexOf(EVENT.api, kinds.lastIndexOf(EVENT.registry)), "the settings event precedes the event that needs them")
})

test("subscribeCommits asks the core for the complete publication while a listener exists, and hands listeners {seq, changes}", async () => {
  const core = new ScriptedCore(ev => ev.kind === EVENT.api ? { notices: [apiResult(ev.id!, { ok: null })] } : undefined)
  const h = await harness(core)
  const modes = () => core.seen.filter(e => e.kind === EVENT.api && e.body.op === "commits").map(e => e.body.on)
  const seen: unknown[] = []
  const off = h.subscribeCommits(p => { seen.push(p) })
  const off2 = h.subscribeCommits(() => {})
  assert.deepEqual(modes(), [true], "the first listener turns it on")
  ;(h as any).onNotice(1, new TextEncoder().encode(JSON.stringify({ seq: 3, entries: [], changes: [{ type: "entry", value: { id: 1 } }] })))
  ;(h as any).onNotice(1, new TextEncoder().encode(JSON.stringify({ seq: 4, entries: [] })))
  assert.deepEqual(seen, [{ seq: 3, changes: [{ type: "entry", value: { id: 1 } }] }])
  off(); assert.deepEqual(modes(), [true])
  off2(); assert.deepEqual(modes(), [true, false], "the last one turns it off")
})

test("close lets a caller's open transaction end before the final close event, and does not wait for a task invocation's", async () => {
  const { core } = line(() => ({}))
  const h = await harness(core)
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  const committing = h.commit(async () => { await gate }, context)
  await new Promise(r => setTimeout(r, 5))
  const closing = h.close(context)
  await new Promise(r => setTimeout(r, 5))
  assert.equal(core.ofKind(EVENT.close).length, 0, "the caller's commit is admitted work: the line drains it first")
  release()
  await committing
  await closing
  const kinds = core.seen.map(e => e.kind)
  assert.ok(kinds.indexOf(EVENT.close) > kinds.lastIndexOf(EVENT.tx), "the close event follows the commit's transaction events")

  const { core: other } = line(() => ({}))
  const h2 = await harness(other)
  await (h2 as any).client.begin({ invocation: 5 })
  await h2.close(context)
  assert.equal(other.ofKind(EVENT.close).length, 1, "an invocation's transaction is aborted by the close event, not awaited")
})

test("inspect() hands a blocked task's error over as an Error", async () => {
  const body = { scheduling: "paused", submissions: [], tasks: [{ record: { id: 3 }, state: { kind: "blocked", reason: "migration_failed", error: { name: "Error", message: "no migration" } } }, { record: { id: 4 }, state: { kind: "ready", migrates: false } }] }
  const core = new ScriptedCore(ev => ev.kind === EVENT.inspect ? { notices: [apiResult(ev.body.requestId, { ok: body })] } : undefined)
  const h = await harness(core)
  const inspection: any = await h.inspect(context)
  assert.ok(inspection.tasks[0].state.error instanceof Error)
  assert.equal(inspection.tasks[0].state.error.message, "no migration")
  assert.deepEqual(inspection.tasks[1].state, { kind: "ready", migrates: false })
})

test("open rejects a registry without the built-in tasks, as Harness.open does", async () => {
  const { core } = line(() => ({}))
  const full = createRegistry()
  const registry = { subscribe: full.subscribe.bind(full), snapshot: () => { const s = full.snapshot(); return Object.assign(Object.create(s), { task: (name: string) => name === "pi.generation" ? undefined : s.task(name) }) } }
  await assert.rejects(harness(core, { registry }), /Registry lacks built-in tasks pi.generation; create it with createRegistry\(\)/)
  assert.equal(core.ofKind(EVENT.open).length, 0, "nothing reaches the core")
  const { core: session } = line(() => ({}))
  await harness(session, { registry, mode: "session" })
  assert.equal(session.ofKind(EVENT.open).length, 1, "a Session has no built-in tasks to require")
})

test("a report notice from the core reaches HarnessOptions.onReport as an Error", async () => {
  const reports: Error[] = []
  const core = new ScriptedCore(ev => ev.kind === EVENT.api && ev.body.op !== "opened" ? { notices: [{ kind: NOTICE.report, payload: new TextEncoder().encode(JSON.stringify({ name: "RangeError", message: "no sandbox" })) }, apiResult(ev.id!, { ok: null })] } : undefined)
  const h = await harness(core, { onReport: (error: Error) => reports.push(error) })
  await h.snapshot(AgentDoc, 1, context)
  assert.equal(reports.length, 1)
  assert.ok(reports[0] instanceof Error)
  assert.equal(reports[0]!.message, "no sandbox")
  assert.equal(reports[0]!.name, "RangeError")
})

test("the resolved agent names the hooks of its selected extensions, in extension order, as handler indices", async () => {
  const { agentJSON } = await import("../src/facade/agent.ts")
  const before = () => undefined, after = () => undefined
  const ids = new Map<unknown, number>()
  const id = (o: unknown) => { if (!ids.has(o)) ids.set(o, ids.size + 1); return ids.get(o)! }
  const agent: any = { thinkingLevel: "off", extensions: [{ name: "a", hooks: [{ task: "pi.tool", handlers: { beforeTool: before, ignored: 3 } }] }, { name: "b", hooks: [{ task: "pi.tool", handlers: { beforeTool: after } }] }, { name: "c" }], tools: [], callable: [], sections: [] }
  const json: any = agentJSON(agent, id)
  assert.deepEqual(json.hooks, [{ task: "pi.tool", handlers: { beforeTool: id(before) } }, { task: "pi.tool", handlers: { beforeTool: id(after) } }])
})

test("the resolved agent's callable tools are encoded like its model tools, and a structuredOutputSchema flags the tool for the core (agent.ts resolveAgent callers)", async () => {
  const { agentJSON, resolveAgent } = await import("../src/facade/agent.ts")
  const run = () => undefined
  const both = { name: "both", execute: run }, programs = { name: "programs", callers: ["tools"], structuredOutputSchema: { type: "object" }, execute: run }, model = { name: "model", callers: ["model"], execute: run }
  const snapshot: any = { installed: () => [{ name: "x", tools: [both, programs, model] }], extension: () => undefined, wraps: () => [] }
  const resolved = resolveAgent({ extensions: ["x"], modelTools: { remove: ["model"] } }, { ...snapshot, extension: (name: string) => (name === "x" ? snapshot.installed()[0] : undefined) }, { extensions: undefined } as any, () => {})
  const ids = new Map<unknown, number>()
  const json: any = agentJSON(resolved, (o: unknown) => { if (!ids.has(o)) ids.set(o, ids.size + 1); return ids.get(o)! })
  assert.deepEqual(json.tools.map((t: any) => t.name), ["both"], "model tools: callers include model, minus modelTools' removals")
  assert.deepEqual(json.callable.map((t: any) => [t.name, t.structuredOutput === true]), [["both", false], ["programs", true]])
})

test("resume() reaches the core in a microtask, as scheduler.ts #kick drains: a read the caller makes synchronously after it comes first", async () => {
  const { core } = line(() => ({}))
  const h = await harness(core)
  h.resume()
  h.resume()
  assert.equal(core.ofKind(EVENT.resume).length, 0, "not synchronously")
  await Promise.resolve()
  assert.equal(core.ofKind(EVENT.resume).length, 1, "once, after the caller's synchronous code")
})

test("a revived plain Error has no own name property, as one thrown in Pi; another name is kept", async () => {
  const { reviveError } = await import("../src/errors.ts")
  assert.deepEqual(Object.keys(reviveError({ name: "Error", message: "x" })), [])
  assert.equal(reviveError({ name: "RangeError", message: "x" }).name, "RangeError")
  assert.equal(reviveError({ name: "Error", message: "x", cause: "missing_task" }).cause, "missing_task", "Error.cause crosses the boundary")
})

test("idle rereads the graph when a task publication lands while the read is in flight, instead of waiting for the next one", async () => {
  let reads = 0
  const live = { tasks: { 1: { conversationId: 1, background: false, conversations: [] } } }
  const core = new ScriptedCore(ev => {
    if (ev.kind !== EVENT.api || ev.body.op !== "taskGraph") return undefined
    reads++
    // The first read's answer comes with the publication that ends the task: the graph it carries is already stale.
    return reads === 1
      ? { notices: [published({ seq: 2, tasks: [{ id: 1, kind: "k", status: "terminal" }] }), apiResult(ev.id!, { ok: live })] }
      : { notices: [apiResult(ev.id!, { ok: { tasks: {} } })] }
  })
  const h = await harness(core)
  await Promise.race([h.idle(undefined, context), new Promise((_, reject) => setTimeout(() => reject(new Error("idle waited for a publication that had already happened")), 200))])
  assert.equal(reads, 2)
})

test("a begin the core refuses rejects the commit instead of waiting forever for ready", async () => {
  const core = new ScriptedCore(ev => ev.kind === EVENT.tx && ev.body.op === "begin" ? { notices: [txResult(ev.id!, { rejected: { name: "Error", message: "Task 7 is terminal" } })] } : undefined)
  const h = await harness(core)
  await assert.rejects(Promise.race([h.commit(() => undefined, context), new Promise((_, reject) => setTimeout(() => reject(new Error("begin never answered")), 200))]), /Task 7 is terminal/)
})

test("operations on a closed Harness reject (a promise), they do not throw", async () => {
  const { core } = line(() => ({}))
  const h = await harness(core)
  const root = new (await import("../src/facade/harness.ts")).Conversation(h, 1)
  await h.close(context)
  const pending = root.waitForIdle(context)
  assert.ok(pending instanceof Promise)
  await assert.rejects(pending, /closed/)
  await assert.rejects(h.waitForIdle(context), /closed/)
})

test("a settings getter that throws does not fail unrelated events", async () => {
  const { core } = line(() => ({}))
  let broken = false
  const h = await harness(core, { settings: { get extensions() { if (broken) throw new Error("settings broke"); return undefined } } })
  broken = true
  h.resume() // sends an event: the sync of live settings must not rethrow
  await h.close(context)
})

test("open names the Storage's profile; the contract hooks' override and an explicit option win, in that order", async () => {
  const { openNodeSqliteStorage, openNodeJsonlStorage, openDurableObjectSqliteStorage } = await import("../src/facade/storage.ts")
  const { useStorageProfile } = await import("../src/facade/harness.ts")
  const profileOf = async (storage: any, extra: Record<string, unknown> = {}) => {
    const { core } = line(() => ({}))
    await Harness.open(storage, { models: models(), registry: NO_EXT, core: { newSession: () => core, sqlTable: [] }, ...extra } as any, context)
    return core.ofKind(EVENT.open)[0]!.body.storageProfile
  }
  assert.equal(await profileOf(new MemoryStorage()), "memory")
  assert.equal(await profileOf(openNodeSqliteStorage(":memory:")), "node-sqlite")
  assert.equal(await profileOf(openNodeJsonlStorage(".")), "jsonl")
  assert.equal(openDurableObjectSqliteStorage({} as any).profile, "cloudflare-do")
  useStorageProfile({ memory: "gate", "node-sqlite": "gate-capsql", jsonl: "gate-capsql" })
  try {
    assert.equal(await profileOf(new MemoryStorage()), "gate", "the contract hooks select the lazy-proxy gate profile for MemoryStorage")
    assert.equal(await profileOf(openNodeSqliteStorage(":memory:")), "gate-capsql", "and the direct capsql profile for the node adapters")
    assert.equal(await profileOf(openNodeJsonlStorage(".")), "gate-capsql")
    assert.equal(await profileOf(new MemoryStorage(), { storageProfile: "node-sqlite" }), "node-sqlite", "an explicit option wins")
  } finally { useStorageProfile(undefined) }
  assert.equal(await profileOf(new MemoryStorage()), "memory")
})

test("a submit and an abort through an invocation-bound context carry the invocation; an unbound context sends none", async () => {
  const { invocationBinding, bindContext } = await import("../src/facade/hostapi.ts")
  const core = new ScriptedCore(ev => {
    if (ev.kind === EVENT.submit) return { notices: [apiResult(ev.body.id, { ok: { id: 5 } })] }
    if (ev.kind === EVENT.abort) return { notices: [apiResult(ev.body.requestId, { ok: "aborted" })] }
    return undefined
  })
  const h = await harness(core)
  const bound = bindContext(invocationBinding(7, new AbortController().signal).binding, context)
  await h.submit(1, { type: "input", content: "late" }, bound)
  await h.submit(1, { type: "input", content: "plain" }, context)
  await h.abortSubmission(5, bound)
  await h.abortSubmission(5, context)
  const submits = core.ofKind(EVENT.submit).map(e => e.body.invocation)
  const aborts = core.ofKind(EVENT.abort).map(e => e.body.invocation)
  assert.deepEqual(submits, [7, undefined], "only the bound submit names its invocation (the core rejects it at line-job start once the invocation is aborted: session.ts:411)")
  assert.deepEqual(aborts, [7, undefined])
})

test("the registrySnapshot hook (scheduler.ts #decide) calls the user's reader once at effect start and answers the snapshot JSON", async () => {
  let calls = 0
  const registry = createRegistry()
  const reader = { snapshot: () => { calls++; return registry.snapshot() }, install: registry.install.bind(registry), subscribe: registry.subscribe.bind(registry) } as any
  let answer: any
  const core = new ScriptedCore(ev => {
    if (ev.kind === EVENT.resume) return { effects: [{ id: 1, kind: EFFECT.hook, payload: new TextEncoder().encode(JSON.stringify({ name: "registrySnapshot", handler: 0, conversationId: 1, taskId: 1, payload: null })) }] }
    if (ev.kind === EVENT.hook_done) { answer = JSON.parse(new TextDecoder().decode(ev.payload.subarray(5))); return undefined }
    if (ev.kind === EVENT.submit) return { notices: [apiResult(ev.body.id, { ok: { id: 5 } })] }
    return undefined
  })
  const h = await harness(core, { registry: reader })
  const before = calls
  await h.submit(1, { type: "input", content: "x" }, context)
  for (let i = 0; i < 5; i++) await Promise.resolve()
  assert.equal(calls - before, 1, "one reader call, made by the hook")
  assert.deepEqual(Object.keys(answer), ["extensions"])
})

test("close() seals the core synchronously with a seal event, before anything else runs; the terminal close follows; a core that rejects seal is harmless", async () => {
  for (const reject of [false, true]) {
    const core = new ScriptedCore(ev => (ev.kind === EVENT.seal && reject ? { status: 1, error: { name: "InvalidEvent", message: "unknown event" } } : undefined))
    const h = await harness(core)
    const seenBefore = core.seen.length
    const closing = h.close(context)
    assert.equal(core.seen[seenBefore]?.kind, EVENT.seal, "the seal event is sent in the close() call itself, not after an await")
    await closing
    assert.deepEqual(core.seen.slice(seenBefore).map(e => e.kind), [EVENT.seal, EVENT.close])
  }
})

test("waitForIdle: an idle scope settles in one microtask pass (scheduler.ts waitForIdle returns an already-resolved promise), a scope the core has not answered stays pending", async () => {
  const answers: { id: number; op: string }[] = []
  const core = new ScriptedCore(ev => {
    if (ev.kind === EVENT.api && ev.body.op === "waitForIdle") answers.push({ id: ev.id!, op: ev.body.op })
    if (ev.kind === EVENT.api && ev.body.op === "waitForIdle" && !deferred.on) return { notices: [apiResult(ev.id!, { ok: null })] }
    return undefined
  })
  const deferred = { on: false }
  const h = await harness(core)
  let passes = 0, counting = true
  const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
  queueMicrotask(spin)
  await h.waitForIdle(context)
  counting = false
  assert.equal(passes, 1, "an idle Harness resolves in one pass")
  assert.deepEqual(answers.map(a => a.op), ["waitForIdle"], "one api request, no taskGraph read")
  // A scope that is not idle stays pending until the core answers.
  deferred.on = true
  let done = false
  const pending = h.waitForIdle(context).then(() => { done = true })
  for (let i = 0; i < 20; i++) await Promise.resolve()
  assert.equal(done, false)
  assert.equal(answers.length, 2, "the second call asked the core too")
  void pending
})
