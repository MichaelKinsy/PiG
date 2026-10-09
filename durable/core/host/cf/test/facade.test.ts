// The pi-durable API facade against a scripted core: which events, transactions and requests each public call produces.
import assert from "node:assert/strict"
import { test } from "node:test"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { EVENT } from "../src/abi.ts"
import { AgentDoc, createRegistry, defineDoc, defineExtension, defineTool, Harness, MemoryStorage, ConversationBusy } from "../src/facade/index.ts"
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
      if (op.op === "end" || op.op === "abort") { log.push(op); return undefined }
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
  const tool = defineTool({ name: "read", description: "Read", parameters: { type: "object" }, replay: "safe", execute: async () => ({ content: [] }) })
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

test("submit sends one submit event with a request ID and resolves when the core publishes the submission; wait settles on the terminal notice", async () => {
  let sent: any
  const core = new ScriptedCore(ev => {
    if (ev.kind === EVENT.submit) { sent = ev.body; return { notices: [published({ submissions: [{ id: 9, requestId: ev.body.requestId, status: "placed" }] })] } }
    if (ev.kind === EVENT.registry) return undefined
    return undefined
  })
  const h = await harness(core)
  const conversation = (await (async () => { core.respond = ev => ev.kind === EVENT.api ? { notices: [apiResult(ev.id!, { ok: { id: 1 } })] } : undefined; return h.conversation(1, context) })())!
  core.respond = ev => {
    if (ev.kind === EVENT.submit) { sent = ev.body; return { notices: [published({ submissions: [{ id: 9, requestId: ev.body.requestId, status: "placed" }] })] } }
    return undefined
  }
  const submission = await conversation.submit({ type: "input", content: "hi" }, context)
  assert.equal(submission.id, 9)
  assert.equal(sent.conversationId, 1)
  assert.equal(sent.type, "input")
  assert.match(sent.requestId, /^[0-9a-f-]{36}$/, "a request ID is minted when the caller gave none")
  assert.ok(core.ofKind(EVENT.registry).length >= 1, "submit resumes scheduling")
  // The settled record is read from Pi's table by the host; the wait ends on the published notice for this submission.
  ;(h as any).store.run("CREATE TABLE IF NOT EXISTS submissions (id INTEGER PRIMARY KEY, conversation_id INTEGER, request_id TEXT, status TEXT, record TEXT)", [])
  const waiting = submission.wait(context)
  await new Promise(r => setTimeout(r, 10))
  ;(h as any).store.run("INSERT INTO submissions VALUES (9, 1, 'x', 'done', ?)", [JSON.stringify({ id: 9, status: "done" })])
  ;(h as any).onNotice(1, new TextEncoder().encode(JSON.stringify({ submissions: [{ id: 9, requestId: "x", status: "done" }] })))
  assert.deepEqual(await waiting, { id: 9, status: "done" })
})

test("a rejected submit is Pi's error: ConversationBusy", async () => {
  const core = new ScriptedCore(ev => ev.kind === EVENT.submit ? { status: 1, error: { name: "ConversationBusy", message: "Conversation 1 is busy" } } : undefined)
  const h = await harness(core)
  const c = new (await import("../src/facade/harness.ts")).Conversation(h, 1)
  await assert.rejects(c.submit({ type: "input", content: "x", whenBusy: "reject" }, context), (e: Error) => e instanceof ConversationBusy)
})

test("agent() resolves the stored agent state against the live registry in the host, wraps included", async () => {
  const keep = defineTool({ name: "keep", description: "k", parameters: {}, execute: async () => ({ content: [] }) })
  const drop = defineTool({ name: "drop", description: "d", parameters: {}, execute: async () => ({ content: [] }) })
  const registry = createRegistry()
  registry.install(defineExtension({ name: "a", tools: [keep, drop] }))
  const core = new ScriptedCore(ev => ev.kind === EVENT.api && ev.body.op === "snapshot" ? { notices: [apiResult(ev.id!, { ok: { tools: { remove: ["drop"] }, thinkingLevel: "low" } })] } : undefined)
  const h = await harness(core, { registry })
  const agent: any = await h.resolveAgent(1, undefined, context)
  assert.deepEqual(agent.tools.map((t: any) => t.name), ["keep"])
  assert.equal(agent.thinkingLevel, "low")
  assert.equal(core.ofKind(EVENT.api)[0]!.body.doc.definition, "pi.agent")
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
