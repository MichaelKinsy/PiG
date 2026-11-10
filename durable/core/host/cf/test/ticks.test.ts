// Microtask passes a client call spends, against the reference's numbers: this test's own sequence is the scenario of the contract rows F-ticks-seq-gate and
// F-ticks-seq-node (durable/contract/scenarios/ticks-sequence.mjs), run through Pi 1.5.0+c23b9ec1's Harness under the contract hooks, gate profile (SqliteStorage
// over the capsql facade). Every number below is the reference run's printed count: `node durable/contract/cli/contract.mjs run F-ticks-seq-gate --impl native --out DIR`, DIR/ref/F-ticks-seq-gate.trace (Pi) against DIR/native/ (the core). A call that follows another is queued behind the previous line
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
  // Pi v1.5.1: 16, 20, 25: a call queued behind the reserve pass spends the pass of scheduler.ts #planCandidates (19 and 24 at c23b9ec1 as b556cfe5; F-ticks-seq-gate `wait (settled)`, `wait (settled, again)`, `entries`; e7a0df27 counted 14, 17, 19: a read of a conversation's entries waits for the key gates of the group-commit Session). A job queued behind the previous commit job's tail starts a tick later than one on an idle line.
  assert.deepEqual({ settled, again, entries }, { settled: 16, again: 20, entries: 25 })
  // The other reads of the same scenario (Pi c23b9ec1, as b556cfe5; F-ticks-seq-gate): inspect is readOnLine(tasks.inspect, two scanAll pages) returned as it is
  // (harness.ts); getTask, status, get and conversation are one single-statement line read, the last three awaited by an async
  // method (harness.ts, submissions.ts).
  assert.deepEqual({ fresh, conversation, getTask, missing, status, get }, { fresh: 28, conversation: 13, getTask: 16, missing: 16, status: 13, get: 13 })
  // submit on a cold conversation: the admission commit through admitSubmission, whose `await prepareBoundary` and `await startRun` resume one tick
  // after those async functions return (submissions.ts); Pi c23b9ec1 counts 99 (measured, F-ticks-seq-gate `submit (cold conversation)`; b556cfe5 counted 101, e7a0df27 counted 102: the attempt delta commit no longer commits alone): Submissions.submit returns the span of pi.durable.submit.
  assert.equal(submit, 99)
  // wait on a submission whose turn is running: the whole generation lifecycle, from the admission commit to the answer commit. Every
  // await of generation.ts between its commits spends its tick: the helper async functions of a commit body (prepareBoundary, appendAssistant,
  // applyBoundary), the settle's reads (transaction.ts), the run's context read (context.ts, scheduler.ts), hooks.each (scheduler.ts),
  // ensureProviderSessionId (provider.ts), the stream's end and the start of a commit queued on an idle line (session.ts). Pi 1.2.0 counted 409; Pi 8b0caefc publishes a commit when Storage adopts it, hopcost.AdoptTail (3 over capsql) ticks before #runCommit resumes, so the wait on the last commit's publication resolves that much earlier: 406.
  // Pi c23b9ec1 counts 346 (b556cfe5 counted 329, e7a0df27 counted 411; the generation's attempt and the tool slots' running deltas ride with the next commit, e5c6182b), measured as the `wait (running)` line of the reference run of F-ticks-seq-gate.
  // The core's `wait (running)` of the same row, through `contract run F-ticks-seq-gate --impl native`, equals it.
  assert.equal(running, 346)
  // snapshot of a document that does not exist: Session.snapshot awaits the line load once and returns (session.ts); the migration retry of
  // the host's read settles the same promise, so a success spends no pass of its own (Pi b556cfe5 counts 18, F-ticks-seq-gate `snapshot of a missing document`; e7a0df27 counted 13; the `.catch` adds one in both).
  assert.equal(snapshot, 18)
})
