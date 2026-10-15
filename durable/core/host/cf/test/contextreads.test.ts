import assert from "node:assert/strict"
import { existsSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import { test } from "node:test"
import { readContextFrom, type EntryRecord } from "../src/context.ts"
import { answerContextRead, CONTEXT_MESSAGES, CONTEXT_RELEASE, CONTEXT_SUMMARY, estimateContext } from "../src/contextreads.ts"
import { ContextReader } from "../src/contextstore.ts"
import { MemorySource, type Conv } from "./memory-source.ts"

const enc = new TextEncoder()
const dec = new TextDecoder()
const corpusDir = join(import.meta.dirname, "../../../history/testdata")
const unhex = (h: string) => Buffer.from(h, "hex").toString("utf8")

// pi-durable main's own estimateContext (and pi-ai's estimateMessageTokens under it) on the views of its corpus: the host's estimate is a port
// of the contract's reference commit (durable/contract/PIN main), and the installed pi-ai is older. Runs when the reference checkout is present (CONTRACT_PI_SOURCE or PI_MAIN).
const piMain = process.env.PI_MAIN ?? process.env.CONTRACT_PI_SOURCE
test("the estimate of the compaction check equals pi-durable's estimateContext on its corpus", { skip: piMain === undefined || !existsSync(piMain) }, async () => {
  process.env.PI_MAIN = piMain
  await import(join(import.meta.dirname, "../../../history/oracle/pimain.mjs"))
  const pi = await import(`${piMain}/packages/durable/src/harness/compaction.ts`) as { estimateContext(view: unknown, extra: unknown[]): number }
  const source = new MemorySource()
  let queries = 0, measured = 0
  for (const line of readFileSync(join(corpusDir, "context.hex"), "utf8").split("\n")) {
    const f = line.split(" ")
    if (f[0] === "C") {
      const rec = JSON.parse(unhex(f[1]!)) as Conv
      source.convs.set(rec.id, rec)
    } else if (f[0] === "E") {
      const list = source.own.get(Number(f[1])) ?? []
      list.push(JSON.parse(unhex(f[3]!)) as EntryRecord)
      source.own.set(Number(f[1]), list)
    } else if (f[0] === "Q" && !f[3]!.startsWith("!")) {
      const at = Number(f[2])
      const { view } = readContextFrom(source, Number(f[1]), at === 0 ? undefined : at, undefined)
      const want = pi.estimateContext(view, [])
      assert.equal(estimateContext(view as never), want, `estimateContext(view, []) of query ${f[1]} ${at}`)
      queries++
      if (want > 0) measured++
    }
  }
  assert.ok(queries > 100 && measured > 100, `${queries} queries, ${measured} with an estimate`)
})

class Rig {
  readonly db = new DatabaseSync(":memory:")
  rowsRead = 0
  constructor() {
    this.db.exec("CREATE TABLE conversations (id INTEGER PRIMARY KEY, record TEXT NOT NULL)")
    this.db.exec("CREATE TABLE entries (id INTEGER PRIMARY KEY, conversation_id INTEGER NOT NULL, head INTEGER, commit_seq INTEGER NOT NULL, record TEXT NOT NULL)")
    this.db.prepare("INSERT INTO conversations (id, record) VALUES (1, '{\"id\":1}')").run()
  }
  all = (sql: string, params: (number | null)[]): unknown[][] => {
    const stmt = this.db.prepare(sql)
    stmt.setReturnArrays(true)
    const rows = stmt.all(...params) as unknown as unknown[][]
    this.rowsRead += rows.length
    return rows.map((r) => r.map((v) => (typeof v === "string" ? enc.encode(v) : v)))
  }
  entry(id: number, kind: string, model: unknown[], head?: number) {
    this.db.prepare("INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, 1, ?, 1, ?)").run(id, head ?? null, JSON.stringify({ id, conversationId: 1, kind, model, ...(head === undefined ? {} : { head }) }))
  }
}
const json = (rows: unknown[][] | undefined) => JSON.parse(dec.decode(rows![0]![0] as Uint8Array))

test("a summary names the system messages the view leads with and the estimate of the compaction check", () => {
  const rig = new Rig()
  const usage = { input: 100, output: 20, cacheRead: 0, cacheWrite: 0, totalTokens: 120, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }
  rig.entry(2, "pi.user", [{ role: "user", content: "hi", timestamp: 1 }])
  rig.entry(3, "pi.system", [{ role: "system", content: "be brief", timestamp: 2 }])
  rig.entry(4, "pi.assistant", [{ role: "assistant", content: [{ type: "text", text: "ok" }], api: "faux", provider: "faux", model: "m", usage, stopReason: "stop", timestamp: 3 }])
  rig.entry(5, "pi.user", [{ role: "user", content: "x".repeat(70), timestamp: 4 }])
  const reader = new ContextReader(rig.all)
  const summary = json(answerContextRead(reader, CONTEXT_SUMMARY, [1, 5, 0, 0, 1]))
  // The system message leads the view (leadWithSystem); the assistant message's usage is the measured prefix and the last user message is estimated: 120 + ceil(70 / 3.5).
  assert.deepEqual(summary, { tail: 5, head: null, system: [{ role: "system", content: "be brief", timestamp: 2 }], tokens: 140 })
  assert.equal(reader.read(1, 0, 5).view.messages[0]!.role, "system")
  assert.equal(json(answerContextRead(reader, CONTEXT_SUMMARY, [1, 5, 0, 0, 0])).tokens, 0, "the estimate is computed only when asked for")
})

test("a head marker's summary lists the active entries and the messages read reuses the range", () => {
  const rig = new Rig()
  rig.entry(2, "pi.user", [{ role: "user", content: "old", timestamp: 1 }])
  rig.entry(3, "pi.user", [{ role: "user", content: "kept", timestamp: 2 }])
  rig.entry(4, "pi.compaction", [{ role: "user", content: "summary", timestamp: 3 }], 3)
  const reader = new ContextReader(rig.all)
  const summary = json(answerContextRead(reader, CONTEXT_SUMMARY, [1, 4, 4, 3, 0]))
  assert.deepEqual(summary, { tail: 4, head: 4, entries: [[4, "pi.compaction"], [3, "pi.user"]], system: [], tokens: 0 })
  const rows = rig.rowsRead
  assert.deepEqual(json(answerContextRead(reader, CONTEXT_MESSAGES, [1, 4, 4, 3])).map((m: { content: string }) => m.content), ["summary", "kept"])
  assert.equal(rig.rowsRead, rows, "the second read of the same bounds read no entries")
})

test("a release drops the kept range, a statement that is not a context read is SQL, and a tail the store lacks is an error", () => {
  const rig = new Rig()
  rig.entry(2, "pi.user", [{ role: "user", content: "a", timestamp: 1 }])
  const reader = new ContextReader(rig.all)
  answerContextRead(reader, CONTEXT_SUMMARY, [1, 2, 0, 0, 0])
  assert.equal(reader.size, 1)
  assert.deepEqual(answerContextRead(reader, CONTEXT_RELEASE, [1]), [])
  assert.equal(reader.size, 0)
  assert.equal(answerContextRead(reader, "SELECT 1", []), undefined)
  assert.throws(() => answerContextRead(reader, CONTEXT_SUMMARY, [1, 9, 0, 0, 0]), /tail 9/)
})

// An edit stored before an entry it targets (edit targets are the caller's and are not checked against the entries): extending the
// kept range by that entry must derive the range again, as Pi's extendRange does (context.ts `previous.edited.has(entry.id)`), or the
// new entry contributes messages its edit omits. The corpora never store such an edit.
test("extending a kept range by an entry an earlier edit targets applies that edit", () => {
  const rig = new Rig()
  rig.db.prepare("INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (2, 1, NULL, 1, ?)").run(JSON.stringify({ id: 2, conversationId: 1, kind: "pi.user", model: [{ role: "user", content: "m2", timestamp: 1 }], edits: [{ target: 3, action: "omit" }] }))
  const reader = new ContextReader(rig.all)
  const contents = (tail: number) => json(answerContextRead(reader, CONTEXT_MESSAGES, [1, tail, 0, 0])).map((m: { content: string }) => m.content)
  assert.deepEqual(contents(2), ["m2"])
  rig.entry(3, "pi.user", [{ role: "user", content: "m3", timestamp: 2 }])
  assert.deepEqual(contents(3), ["m2"], "entry 3 is omitted by entry 2's edit")
})

// The core captured the marker and its head together (context.ts captureContextBounds): a marker head other than the stored one is a fault.
test("a context read whose marker head differs from the stored marker's fails", () => {
  const rig = new Rig()
  rig.entry(2, "pi.user", [{ role: "user", content: "old", timestamp: 1 }])
  rig.entry(3, "pi.user", [{ role: "user", content: "kept", timestamp: 2 }])
  rig.entry(4, "pi.compaction", [{ role: "user", content: "summary", timestamp: 3 }], 3)
  const reader = new ContextReader(rig.all)
  assert.throws(() => answerContextRead(reader, CONTEXT_SUMMARY, [1, 4, 4, 2, 0]), /head marker 4 with head 2/)
  assert.throws(() => answerContextRead(reader, CONTEXT_MESSAGES, [1, 4, 4, 2]), /head marker 4 with head 2/)
  assert.equal(json(answerContextRead(reader, CONTEXT_MESSAGES, [1, 4, 4, 3])).length, 2)
})

// Pi's scheduler keeps a read's range only when its tail is not older than the kept one (scheduler.ts runtime.context): a read
// cut off at an older entry answers from the kept range and leaves it, so the next read through the newest entry scans nothing.
test("a read through an older entry keeps the newer range", () => {
  const rig = new Rig()
  for (let id = 2; id <= 5; id++) rig.entry(id, "pi.user", [{ role: "user", content: `m${id}`, timestamp: id }])
  const reader = new ContextReader(rig.all)
  const contents = (tail: number) => json(answerContextRead(reader, CONTEXT_MESSAGES, [1, tail, 0, 0])).map((m: { content: string }) => m.content)
  assert.deepEqual(contents(5), ["m2", "m3", "m4", "m5"])
  const rows = rig.rowsRead
  assert.deepEqual(contents(3), ["m2", "m3"])
  assert.deepEqual(reader.view(1, 0, 4).messages.map((m) => (m as unknown as { content: string }).content), ["m2", "m3", "m4"], "a view is of the bounds asked for, not of the range kept")
  assert.deepEqual(contents(5), ["m2", "m3", "m4", "m5"])
  assert.equal(rig.rowsRead, rows, "the older reads read nothing and left the newer range kept")
})

// A read that fails keeps nothing: a range whose tail storage lacks would make a later extension start above entries it never read.
test("a read of a tail storage lacks keeps no range", () => {
  const rig = new Rig()
  rig.entry(2, "pi.user", [{ role: "user", content: "m2", timestamp: 1 }])
  const reader = new ContextReader(rig.all)
  const contents = (tail: number) => json(answerContextRead(reader, CONTEXT_MESSAGES, [1, tail, 0, 0])).map((m: { content: string }) => m.content)
  assert.deepEqual(contents(2), ["m2"])
  assert.throws(() => contents(4), /tail 4/)
  for (let id = 3; id <= 5; id++) rig.entry(id, "pi.user", [{ role: "user", content: `m${id}`, timestamp: id }])
  assert.deepEqual(contents(5), ["m2", "m3", "m4", "m5"])
})

// A tail storage lacks, above entries the kept range has not read: the scan finds those entries and the tail check fails. The kept range is
// extended in place, so the check must fail before the extension appends, or the kept range holds entries past its tail and a later
// extension adds them a second time.
test("a read of a tail storage lacks, above entries the kept range has not read, leaves the kept range as it was", () => {
  const rig = new Rig()
  const assistant = (text: string, timestamp: number) => ({ role: "assistant", content: [{ type: "text", text }], stopReason: "stop", timestamp })
  rig.entry(2, "pi.user", [{ role: "user", content: "u2", timestamp: 1 }])
  rig.entry(3, "pi.assistant", [assistant("a3", 2)])
  const reader = new ContextReader(rig.all)
  const contents = (tail: number) => json(answerContextRead(reader, CONTEXT_MESSAGES, [1, tail, 0, 0])).map((m: { content: string | { text: string }[] }) => typeof m.content === "string" ? m.content : m.content[0]!.text)
  assert.deepEqual(contents(3), ["u2", "a3"])
  rig.entry(4, "pi.user", [{ role: "user", content: "u4", timestamp: 3 }])
  rig.entry(5, "pi.assistant", [assistant("a5", 4)])
  assert.throws(() => contents(6), /tail 6/)
  assert.deepEqual(contents(5), ["u2", "a3", "u4", "a5"])
  assert.deepEqual(reader.view(1, 0, 5).entries.map((entry) => entry.id), [2, 3, 4, 5])
})

// A kept range's marker is reused only for the same marker id: a newer marker derives its range again from its own head.
test("a read under a newer head marker derives the range from that marker", () => {
  const rig = new Rig()
  rig.entry(2, "pi.user", [{ role: "user", content: "old", timestamp: 1 }])
  rig.entry(3, "pi.compaction", [{ role: "user", content: "first", timestamp: 2 }], 2)
  rig.entry(4, "pi.user", [{ role: "user", content: "between", timestamp: 3 }])
  rig.entry(5, "pi.compaction", [{ role: "user", content: "second", timestamp: 4 }], 5)
  rig.entry(6, "pi.user", [{ role: "user", content: "new", timestamp: 5 }])
  const reader = new ContextReader(rig.all)
  const contents = (tail: number, head: number, from: number) => json(answerContextRead(reader, CONTEXT_MESSAGES, [1, tail, head, from])).map((m: { content: string }) => m.content)
  assert.deepEqual(contents(4, 3, 2), ["first", "old", "between"])
  assert.deepEqual(contents(6, 5, 5), ["second", "new"])
})
