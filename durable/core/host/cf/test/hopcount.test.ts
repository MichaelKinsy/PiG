// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Crossings per warm turn and what each hop costs (ABI section 5.3 and the `hop` export of section 4.1), counted, not timed: the
// events the core was called with, the steps that carried only a commit, the `hopped` events the core answered with an integer,
// the microtask hops spent. One warm turn runs the same events, so the counts of two warm turns are equal; the ceilings are the
// counts before the hop work (1049 steps, 898 hops chains, 77 `hopped` steps per turn that applied a commit and started the chain for the job's last hops).
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { Type } from "@earendil-works/pi-ai"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider, fauxToolCall } from "@earendil-works/pi-ai/providers/faux"
import { Harness } from "../src/facade/harness.ts"
import { createRegistry, defineExtension, defineTool } from "../src/facade/index.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { EVENT } from "../src/abi.ts"
import type { Step } from "../src/wire.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

type Counts = { steps: number; hopped: number; hoppedCommit: number; hoppedEmpty: number; hoppedHopsOnly: number; hopChains: number; hopTicks: number; hopsThin: number; commits: number; statements: string[] }

/** One eight-lookup turn, W1's shape: the model calls `lookup` eight times, one after another, then answers. */
async function turns(count: number, thin: boolean): Promise<Counts[]> {
  const module = new (WebAssembly.Module as unknown as new (b: Uint8Array) => WebAssembly.Module)(readFileSync(join(root, "dist", `core-${toolchain}.wasm`)))
  const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
  const models = createModels()
  models.setProvider(faux.provider)
  let calls = 0
  const lookup = defineTool({ name: "lookup", description: "Look up record number n", parameters: Type.Object({ n: Type.Number() }), execute: async ({ n }: { n: number }) => ({ output: [{ type: "text", text: `record ${n}` }] }) })
  const registry = createRegistry()
  registry.install(defineExtension({ name: "bench", tools: [lookup] }))
  const respond = (transcript: { messages: { role: string }[] }): ReturnType<typeof fauxAssistantMessage> => {
    faux.appendResponses([respond as never])
    const lastUser = transcript.messages.findLastIndex(m => m.role === "user")
    const done = transcript.messages.slice(lastUser + 1).filter(m => m.role === "toolResult").length
    return done < 8 ? fauxAssistantMessage(fauxToolCall("lookup", { n: ++calls }, { id: `c${calls}` }), { stopReason: "toolUse" }) : fauxAssistantMessage("done")
  }
  faux.setResponses([respond as never])
  const harness = await Harness.open(await openNodeSqliteStorage(join(tempDir("hopcount"), "h.sqlite")), { models, registry, core: module, storageProfile: "gate-capsql" } as never, context)
  const host = (harness as unknown as { host: { core: any; store: any; counters: any } }).host
  const session = host.core
  const seen = { steps: 0, hopped: 0, hoppedCommit: 0, hoppedEmpty: 0, hoppedHopsOnly: 0 }
  const statements: string[] = []
  const classify = (kind: number, step: Step | 0 | 1) => {
    seen.steps++
    if (kind !== EVENT.hopped) return
    seen.hopped++
    if (step === 0) seen.hoppedEmpty++
    else if (step === 1) seen.hoppedHopsOnly++
    else if (step.commits.length > 0) seen.hoppedCommit++
    else if (step.commits.length + step.notices.length + step.reads.length === 0 && step.effects.length === 0) seen.hoppedEmpty++
    else if (step.commits.length + step.notices.length + step.reads.length === 0 && step.effects.length === 1 && step.effects[0]!.kind === 14) seen.hoppedHopsOnly++
  }
  const step = session.step.bind(session), hop = session.hop?.bind(session)
  session.step = (kind: number, payload: Uint8Array | undefined, now: number) => { const out = step(kind, payload, now); classify(kind, out); return out }
  if (thin && hop) session.hop = (id: number, now: number) => { const out = hop(id, now); classify(EVENT.hopped, out); return out }
  else session.hop = undefined
  const run = host.store.run.bind(host.store)
  // Wall-clock timestamps and durations and the faux provider's random api name differ between two runs of the same events; nothing else does.
  const stable = (text: string) => text.replace(/faux:[0-9a-z]+:[0-9a-z]+/g, "faux").replace(/\b1[0-9]{12}\b/g, "T").replace(/durationMs\\*":[0-9]+/g, "durationMs")
  host.store.run = (sql: string, params: unknown[]) => { statements.push(stable(`${sql}|${JSON.stringify(params, (_, v) => (v instanceof Uint8Array ? [...v] : v))}`)); return run(sql, params) }
  const r = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })
  const out: Counts[] = []
  for (let i = 0; i < count; i++) {
    for (const k of Object.keys(seen)) (seen as Record<string, number>)[k] = 0
    statements.length = 0
    const before = structuredClone(host.counters)
    const submission = await r.submit({ type: "input", content: `turn ${i}` }, context)
    await submission.wait(context)
    await r.waitForIdle(context)
    const c = host.counters
    out.push({ ...seen, hopChains: c.hopChains - before.hopChains, hopTicks: c.hopTicks - before.hopTicks, hopsThin: c.hopsThin - before.hopsThin, commits: c.commits - before.commits, statements: [...statements] })
  }
  await harness.close(context)
  return out
}

test("a line job's statements ride on its hops chain: no hopped step carries a commit", async () => {
  const [, ...warm] = await turns(4, true)
  assert.ok(warm.every(w => w.commits > 0), "a turn commits")
  for (const w of warm) assert.equal(w.hoppedCommit, 0, "the commit a line job parks for its COMMIT hop is applied by its chain, not by a step of its own")
})

test("warm turns run the same events: equal counts, under the ceilings of the hop work", async () => {
  const [, a, b, c] = await turns(4, true)
  for (const w of [b!, c!]) assert.deepEqual({ ...w, statements: w.statements.length }, { ...a!, statements: a!.statements.length })
  assert.ok(a!.steps <= 980, `${a!.steps} steps per warm turn (1049 before)`)
  assert.ok(a!.hopped <= 830, `${a!.hopped} hopped events per warm turn (898 before)`)
  assert.equal(a!.hopTicks, 6776, "the chains spend the microtask hops Pi's awaits spend: fusing chains moves no tick (6,776 at Pi 1.3.0)")
})

test("the hop export answers an integer for a step the host would not decode, and the run is the same either way", async () => {
  const [, thin] = await turns(2, true)
  const [, slow] = await turns(2, false)
  assert.ok(thin!.hopsThin > 0 && thin!.hoppedEmpty + thin!.hoppedHopsOnly >= thin!.hopsThin, "integer answers are counted")
  assert.equal(slow!.hopsThin, 0, "a core without hop takes step for every hopped event")
  assert.equal(thin!.hoppedHopsOnly + thin!.hoppedEmpty, slow!.hoppedHopsOnly + slow!.hoppedEmpty, "the same steps are empty or hops-only on both paths")
  assert.equal(thin!.hopChains, slow!.hopChains)
  assert.equal(thin!.hopTicks, slow!.hopTicks)
  assert.deepEqual(thin!.statements, slow!.statements, "the same statements are applied in the same order")
})
