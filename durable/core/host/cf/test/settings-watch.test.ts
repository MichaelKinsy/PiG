// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// The live-settings change detector (facade/settings-watch.ts) against the exact comparison it replaces: the JSON text of
// `{...resolveSettings(raw), extensions: names}`. The fast path may answer `true` only when the texts are equal; it may answer `false`
// for anything it cannot prove. Every live mutation of every field must be seen.
import assert from "node:assert/strict"
import { constants, PerformanceObserver } from "node:perf_hooks"
import { test } from "node:test"
import { resolveSettings } from "../src/facade/agent.ts"
import { SentSettings } from "../src/facade/settings-watch.ts"

const wire = (raw: any) => { const s = resolveSettings(raw); return JSON.stringify({ ...s, extensions: s.extensions?.map((e: { name: string }) => e.name) }) }
const sentOf = (raw: any) => { const s = new SentSettings(); s.set(wire(raw)); return s }

const ext = (name: string) => ({ name, hooks: () => {} })
const full = () => ({
  extensions: [ext("a"), ext("b")],
  stream: { temperature: 0.5, headers: { x: "1" }, tags: ["a", "b"] },
  retry: { maxRetries: 5, extra: true },
  compaction: { enabled: false, reserveTokens: 100 },
  progress: { partialIntervalMs: 7, outputIntervalMs: 9 },
  contextRetentionMs: 1234, toolExecution: "sequential", steeringMode: "all", followUpMode: "all", documentRetentionMs: 5,
})

test("unchanged settings are proven unchanged without a text comparison (empty, defaults and fully populated)", () => {
  for (const raw of [undefined, null, {}, { documentRetentionMs: 0 }, { documentRetentionMs: Infinity }, { stream: undefined }, { retry: {} }, full(), { extensions: [] }, { retry: { enabled: undefined } }]) {
    const sent = sentOf(raw)
    assert.equal(sent.unchanged(raw), true, JSON.stringify(raw))
  }
})

/** Every live mutation of one field of a populated settings object: the detector must say "changed" exactly when the text changes. */
const mutations: [string, (r: any) => void][] = [
  ["extensions add", r => r.extensions.push(ext("c"))],
  ["extensions rename", r => { r.extensions[1] = ext("z") }],
  ["extensions drop", r => { r.extensions.length = 1 }],
  ["extensions unset", r => { delete r.extensions }],
  ["stream value", r => { r.stream.temperature = 0.6 }],
  ["stream nested", r => { r.stream.headers.x = "2" }],
  ["stream array", r => { r.stream.tags.push("c") }],
  ["stream key added", r => { r.stream.top_p = 1 }],
  ["stream key removed", r => { delete r.stream.temperature }],
  ["stream replaced", r => { r.stream = undefined }],
  ["retry default overridden", r => { r.retry.baseDelayMs = 1 }],
  ["retry extra", r => { r.retry.extra = false }],
  ["retry default unset by undefined", r => { r.retry.maxAgentDelayMs = undefined }],
  ["retry removed", r => { delete r.retry }],
  ["compaction value", r => { r.compaction.reserveTokens = 101 }],
  ["compaction enabled", r => { r.compaction.enabled = true }],
  ["progress partial", r => { r.progress.partialIntervalMs = 8 }],
  ["progress output", r => { r.progress.outputIntervalMs = 10 }],
  ["progress removed", r => { delete r.progress }],
  ["contextRetentionMs", r => { r.contextRetentionMs = 1 }],
  ["toolExecution", r => { r.toolExecution = "parallel" }],
  ["steeringMode", r => { r.steeringMode = "one-at-a-time" }],
  ["followUpMode", r => { r.followUpMode = "one-at-a-time" }],
  ["documentRetentionMs", r => { r.documentRetentionMs = 0 }],
  ["documentRetentionMs to the default", r => { r.documentRetentionMs = 600_000 }],
  ["documentRetentionMs unset", r => { delete r.documentRetentionMs }],
  ["documentRetentionMs Infinity", r => { r.documentRetentionMs = Infinity }],
  ["documentRetentionMs not a number", r => { r.documentRetentionMs = "soon" }],
  ["new unrelated top key", r => { r.unrelated = 1 }],
]

for (const [name, mutate] of mutations) {
  test(`a live change is seen: ${name}`, () => {
    const raw = full()
    const sent = sentOf(raw)
    mutate(raw)
    const changed = wire(raw) !== sent.text
    // the property: "unchanged" only when the text is equal
    if (sent.unchanged(raw)) assert.equal(changed, false, `${name}: the detector missed a live change`)
    // and where the text differs the fast path never hides it
    if (changed) assert.equal(sent.unchanged(raw), false)
    // a repeat of the same state with the sent copy refreshed is unchanged again
    const again = new SentSettings(); again.set(wire(raw)); assert.equal(again.unchanged(raw), true, `${name}: unchanged after refresh`)
  })
}

test("values the fast path cannot prove fall back to the text comparison (never a false unchanged)", () => {
  const odd: any[] = [
    { stream: { when: new Date(0) } }, { stream: { n: NaN } }, { stream: { n: Infinity } }, { stream: { f: () => 1 } },
    { stream: { u: undefined } }, { stream: { arr: [undefined] } }, { stream: Object.create({ inherited: 1 }) },
    { stream: "ab" }, { stream: [1] }, { extensions: null }, { extensions: [{ name: 1 }] }, { toolExecution: 7 },
    { retry: new (class R { x = 1 })() }, { progress: { partialIntervalMs: null } }, { contextRetentionMs: -0 },
  ]
  for (const raw of odd) {
    let text: string | undefined
    try { text = wire(raw) } catch { continue }
    const sent = new SentSettings(); sent.set(text)
    if (sent.unchanged(raw)) assert.equal(wire(raw), sent.text, JSON.stringify(Object.keys(raw)))
  }
})

test("a throwing getter is not a change and never throws out of the detector", () => {
  const raw = { get stream(): never { throw new Error("boom") } }
  const sent = new SentSettings(); sent.set(wire({}))
  assert.equal(sent.unchanged(raw), false)
})

test("a randomized live mutation sequence never hides a change", () => {
  let seed = 12345
  const rnd = (n: number) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n }
  const raw: any = full()
  const sent = sentOf(raw)
  const values: any[] = [undefined, null, 0, 1, -0, 2.5, "a", "b", true, false, {}, [], { k: 1 }, [1], NaN]
  const keys = ["stream", "retry", "compaction", "progress", "contextRetentionMs", "toolExecution", "steeringMode", "followUpMode", "extensions"]
  let seen = 0, proven = 0
  for (let step = 0; step < 4000; step++) {
    const key = keys[rnd(keys.length)] as string
    const target = rnd(3) === 0 || typeof raw[key] !== "object" || raw[key] === null || Array.isArray(raw[key]) ? raw : raw[key]
    const sub = target === raw ? key : ["maxRetries", "enabled", "partialIntervalMs", "outputIntervalMs", "temperature", "extra"][rnd(6)] as string
    if (rnd(5) === 0) delete target[sub]; else target[sub] = values[rnd(values.length)]
    let text: string
    try { text = wire(raw) } catch { Object.assign(raw, full()); continue }
    const changed = text !== sent.text
    if (sent.unchanged(raw)) { proven++; assert.equal(changed, false, `step ${step}: ${key}.${sub}`) }
    if (changed) { sent.set(text); seen++ }
  }
  assert.ok(seen > 100, `the mutations changed the settings ${seen} times`)
  assert.ok(proven > 100, `the fast path proved "unchanged" ${proven} times: it must not be trivially false`)
})

/** Minor collections V8 reports while `run` executes: the detector allocates nothing, the text comparison it replaces allocates a resolved object and a string per call. */
async function collections(run: () => void): Promise<number> {
  let count = 0
  const observer = new PerformanceObserver(list => { count += list.getEntries().filter(e => (e as unknown as { detail: { kind: number } }).detail.kind === constants.NODE_PERFORMANCE_GC_MINOR).length })
  observer.observe({ entryTypes: ["gc"] })
  run()
  await new Promise(resolve => setImmediate(resolve))
  await new Promise(resolve => setImmediate(resolve))
  observer.disconnect()
  return count
}

test("the detector allocates nothing per send (no collection over a million calls; the text comparison it replaces collects)", async () => {
  // Flat records, the usual settings: a record nested inside a value is compared with its key lists (two small arrays), which is the exact order check.
  const raw = { extensions: [ext("a"), ext("b")], stream: { seed: 5, maxTokens: 100, tags: ["a"] }, retry: { maxRetries: 5, extra: true }, compaction: { enabled: false }, progress: { partialIntervalMs: 7 }, toolExecution: "sequential" }
  const sent = sentOf(raw)
  let proven = 0
  const calls = 1_000_000
  await collections(() => { for (let i = 0; i < 100_000; i++) sent.unchanged(raw) }) // warm-up: compilation and the test's own garbage settle first
  // The least of three runs: a collection the runtime starts for earlier tests' garbage lands in one run, an allocating detector in all.
  const runs: number[] = []
  for (let r = 0; r < 3; r++) runs.push(await collections(() => { for (let i = 0; i < calls; i++) if (sent.unchanged(raw)) proven++ }))
  const detector = Math.min(...runs)
  assert.equal(proven, 3 * calls)
  let equal = 0
  const text = await collections(() => { for (let i = 0; i < calls; i++) if (wire(raw) === sent.text) equal++ })
  assert.equal(equal, calls)
  assert.ok(text > 0, "the control (JSON.stringify per send) must trigger collections for the measurement to mean anything")
  assert.equal(detector, 0, `the detector triggered ${runs} minor collections over ${calls} calls`)
})

test("an inherited or non-enumerable override is not one: spread leaves the default, so a change to it is seen", () => {
  const nonEnumerable = (key: string, value: unknown) => Object.defineProperty({}, key, { value, enumerable: false })
  for (const [field, key, value] of [["retry", "maxRetries", 9], ["compaction", "reserveTokens", 100]] as const) {
    const sent = sentOf({ [field]: { [key]: value } })
    for (const over of [Object.create({ [key]: value }), nonEnumerable(key, value)]) {
      const raw = { [field]: over }
      assert.notEqual(wire(raw), sent.text, `${field}.${key}: the resolved settings changed`)
      assert.equal(sent.unchanged(raw), false, `${field}.${key} held by the prototype or non-enumerable`)
    }
  }
})
