// Microtask passes a client call spends, against the reference's numbers: this test's own sequence run through Pi 1.2.0's Harness under
// the contract hooks, gate profile (SqliteStorage over the capsql facade). A call that follows another is queued behind the previous line
// job: scheduler.resume() queues a reserve pass (a commit job) on the Session line, and session.ts #enqueue starts the next job through
// `#tail.then(job)`, where the tail is `run.then(...)` of the previous job, so a job queued behind a commit job starts two ticks after that run settles.
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { BACKGROUND_CONTEXT as context } from "@earendil-works/chord/context"
import { createModels } from "@earendil-works/pi-ai/models"
import { fauxAssistantMessage, fauxProvider } from "@earendil-works/pi-ai/providers/faux"
import { createRegistry } from "../src/facade/index.ts"
import { Harness } from "../src/facade/harness.ts"
import { openNodeSqliteStorage } from "../src/facade/storage.ts"
import { root, tempDir, toolchain } from "./helpers.ts"

const bytes = () => readFileSync(join(root, "dist", `core-${toolchain}.wasm`))

test("a call queued behind the reserve pass of the previous call's resume() starts one tick after the tail resolves", async () => {
  const module = new (WebAssembly.Module as unknown as new (b: Uint8Array) => WebAssembly.Module)(bytes())
  const faux = fauxProvider({ tokenSize: { min: 1e9, max: 1e9 } })
  const models = createModels()
  models.setProvider(faux.provider)
  faux.setResponses([() => fauxAssistantMessage("answer")])
  const harness = await Harness.open(await openNodeSqliteStorage(join(tempDir("ticks"), "ticks.sqlite")), { models, registry: createRegistry(), core: module, storageProfile: "gate-capsql" } as any, context)
  const root_ = await harness.root(context, { agent: { model: { provider: "faux", modelId: "faux-1" } } })
  let passes = 0, counting = false
  const spin = () => { if (counting) { passes++; queueMicrotask(spin) } }
  let last: any
  const measure = async <T,>(call: () => Promise<T>) => { passes = 0; counting = true; queueMicrotask(spin); last = await call(); counting = false; return passes }
  const fresh = await measure(() => harness.inspect(context))
  const conversation = await measure(() => harness.conversation(root_.id, context))
  const submit = await measure(() => root_.submit({ type: "input", content: "first" }, context))
  const first = last
  const running = await measure(() => first.wait(context))
  await new Promise(resolve => setTimeout(resolve, 100))
  const settled = await measure(() => first.wait(context))
  const again = await measure(() => first.wait(context))
  const entries = await measure(() => root_.entries({}, 5, undefined, context))
  const getTask = await measure(() => harness.getTask(1, context))
  const missing = await measure(() => harness.getTask(999, context))
  const status = await measure(() => first.status(context))
  const get = await measure(() => harness.submission(first.id, context))
  const snapshot = await measure(() => (harness as any).snapshot({ definition: { kind: "diff.none", version: 1, scope: "conversation", history: "latest", initial: () => ({}) } }, root_.id, context).catch(() => undefined))
  await harness.close(context)
  // Pi 1.2.0: 14, 17, 19. A job queued behind the previous commit job's tail starts a tick later than one on an idle line.
  assert.deepEqual({ settled, again, entries }, { settled: 14, again: 17, entries: 19 })
  // The other reads of the same scenario (Pi 1.2.0): inspect is readOnLine(tasks.inspect, two scanAll pages) returned as it is
  // (harness.ts); getTask, status, get and conversation are one single-statement line read, the last three awaited by an async
  // method (harness.ts, submissions.ts).
  assert.deepEqual({ fresh, conversation, getTask, missing, status, get }, { fresh: 25, conversation: 11, getTask: 10, missing: 10, status: 11, get: 11 })
  // submit on a cold conversation: the admission commit through admitSubmission, whose `await prepareBoundary` and `await startRun` resume one tick
  // after those async functions return (submissions.ts); Pi 1.2.0 counts 100.
  assert.equal(submit, 100)
  // wait on a submission whose turn is running: the whole generation lifecycle, from the admission commit to the answer commit. Every
  // await of generation.ts between its commits spends its tick: the helper async functions of a commit body (prepareBoundary, appendAssistant,
  // applyBoundary), the settle's reads (transaction.ts), the run's context read (context.ts, scheduler.ts), hooks.each (scheduler.ts),
  // ensureProviderSessionId (provider.ts), the stream's end and the start of a commit queued on an idle line (session.ts). Pi 1.2.0 counts 409.
  assert.equal(running, 409)
  // snapshot of a document that does not exist: Session.snapshot awaits the line load once and returns (session.ts); the migration retry of
  // the host's read settles the same promise, so a success spends no pass of its own (Pi 1.2.0 counts 13; the `.catch` adds one in both).
  assert.equal(snapshot, 13)
})
