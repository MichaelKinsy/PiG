import assert from "node:assert/strict"
import { createHash } from "node:crypto"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import { test } from "node:test"
import { readContextFrom, readContext, type EntryRecord } from "../src/context.ts"
import { answerContextRead, CONTEXT_SUMMARY } from "../src/contextreads.ts"
import { ContextSegments, SEGMENT_BYTES } from "../src/contextsegments.ts"
import { ContextReader } from "../src/contextstore.ts"
import { MemorySource, type Conv } from "./memory-source.ts"

// Checkpoints are a cache of a pure derivation: every test compares the context read with a checkpoint to the one derived from
// the entries (the TS derivation the corpus tests prove equal to pi-durable main's), and every way a checkpoint can be wrong must
// leave the answer as it was.
const unhex = (h: string) => Buffer.from(h, "hex").toString("utf8")

class Rig {
  readonly db = new DatabaseSync(":memory:")
  readonly memory = new MemorySource()
  /** Entry records the page statement returned, and statements that read a checkpoint table, since the last reset. */
  entryRows = 0
  checkpointReads = 0
  failOn: RegExp | undefined
  next = 1
  constructor() {
    this.db.exec("CREATE TABLE conversations (id INTEGER PRIMARY KEY, record TEXT NOT NULL)")
    this.db.exec("CREATE TABLE entries (id INTEGER PRIMARY KEY, conversation_id INTEGER NOT NULL, head INTEGER, commit_seq INTEGER NOT NULL, record TEXT NOT NULL)")
    this.db.exec("CREATE TABLE durable_schema (singleton INTEGER PRIMARY KEY CHECK (singleton = 1), version INTEGER NOT NULL)")
    this.db.exec("INSERT INTO durable_schema (singleton, version) VALUES (1, 7)")
  }
  all = (sql: string, params: (number | null)[]): unknown[][] => {
    const stmt = this.db.prepare(sql)
    stmt.setReturnArrays(true)
    const rows = stmt.all(...(params as never[])) as unknown as unknown[][]
    if (sql.includes("group_concat") && rows[0]![1] !== null) this.entryRows += Number(rows[0]![1])
    if (sql.includes("FROM pig_context_checkpoints") && rows.length > 0) this.checkpointReads++
    return rows
  }
  run = (sql: string, params: (number | string | Uint8Array | null)[]): number => {
    if (this.failOn?.test(sql)) throw new Error(`injected failure: ${sql}`)
    return Number(this.db.prepare(sql).run(...(params as never[])).changes)
  }
  tx = (work: () => void) => {
    this.db.exec("BEGIN IMMEDIATE")
    try { work(); this.db.exec("COMMIT") } catch (e) { this.db.exec("ROLLBACK"); throw e }
  }
  segments() { return new ContextSegments({ all: this.all, run: this.run, tx: this.tx }) }
  /** A reader that writes a checkpoint at every read that settled a message. */
  reader(on = true) { return new ContextReader(this.all, on ? this.segments() : undefined, 1, 1) }
  conversation(id: number, parent?: { conversationId: number; at: number }) {
    const rec = { id, ...(parent ? { parent } : {}) }
    this.db.prepare("INSERT INTO conversations (id, record) VALUES (?, ?)").run(id, JSON.stringify(rec))
    this.memory.convs.set(id, rec)
  }
  entry(conv: number, kind: string, model: unknown[] | undefined, extra: Record<string, unknown> = {}): number {
    const id = this.next++
    const rec = { kind, ...(model ? { model } : {}), id, conversationId: conv, ...extra } as EntryRecord
    this.db.prepare("INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, ?, ?)").run(id, conv, (extra.head as number | undefined) ?? null, id, JSON.stringify(rec))
    const list = this.memory.own.get(conv) ?? []
    list.push(rec)
    this.memory.own.set(conv, list)
    return id
  }
  turn(conv: number, n: number, size = 0): number {
    const first = this.entry(conv, "pi.user", [{ role: "user", content: `question ${n}`, timestamp: n }])
    this.entry(conv, "pi.assistant", [{ role: "assistant", content: [{ type: "toolCall", id: `call_${n}`, name: "read", arguments: {} }], stopReason: "toolUse", timestamp: n }])
    this.entry(conv, "pi.tool-result", [{ role: "toolResult", toolCallId: `call_${n}`, toolName: "read", content: [{ type: "text", text: `result ${n} ${"x".repeat(size)}` }], isError: false, timestamp: n }])
    this.entry(conv, "pi.assistant", [{ role: "assistant", content: [{ type: "text", text: `answer ${n}` }], stopReason: "stop", timestamp: n }])
    return first
  }
  tail() { return this.next - 1 }
  count(table: string) {
    try { return Number((this.db.prepare(`SELECT count(*) AS n FROM ${table}`).get() as { n: number }).n) } catch { return 0 }
  }
}

const expected = (rig: Rig, conv: number, at?: number) => readContext(rig.memory, conv, at).messages

/** A store with a checkpoint written for a conversation of 60 turns, and a fresh reader's way to read it. */
function checkpointed() {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 60; i++) rig.turn(2, i)
  const writer = rig.reader()
  writer.messages(2, 0, rig.tail())
  assert.equal(rig.count("pig_context_checkpoints"), 1)
  const read = (at = rig.tail(), reader = rig.reader()) => {
    rig.entryRows = 0
    return { messages: reader.messages(2, 0, at), rows: rig.entryRows }
  }
  return { rig, read }
}

test("a cold reader resumes from the checkpoint, reads only the entries after it, and derives what the entries derive", () => {
  const { rig, read } = checkpointed()
  const same = read()
  assert.deepEqual(same.messages, expected(rig, 2))
  assert.equal(same.rows, 0, "the checkpoint holds the whole context")
  for (let i = 60; i < 70; i++) rig.turn(2, i)
  const later = read()
  assert.deepEqual(later.messages, expected(rig, 2))
  assert.equal(later.rows, 40, "only the entries after the checkpoint are read")
  assert.equal(rig.reader(false).messages(2, 0, rig.tail()).length, later.messages.length)
})

test("a checkpoint that is wrong in any way is ignored and the entries are derived", async (t) => {
  const mutations: [string, (rig: Rig) => void][] = [
    ["a corrupt segment", (rig) => rig.db.exec("UPDATE pig_context_segments SET messages = CAST('[{\"role\":' AS BLOB)")],
    ["a segment of other messages", (rig) => rig.db.exec("UPDATE pig_context_segments SET messages = CAST('[]' AS BLOB)")],
    ["a segment that does not hold the messages it counts", (rig) => rig.db.exec("UPDATE pig_context_segments SET count = count + 1")],
    ["segments numbered from another index", (rig) => rig.db.exec("UPDATE pig_context_segments SET idx = idx + 1")],
    ["a missing segment", (rig) => rig.db.exec("DELETE FROM pig_context_segments")],
    ["a header that counts other segments", (rig) => rig.db.exec("UPDATE pig_context_checkpoints SET segments = segments + 1")],
    ["a header that counts other messages", (rig) => rig.db.exec("UPDATE pig_context_checkpoints SET settled = settled + 1")],
    ["a header with a corrupt open list", (rig) => rig.db.exec("UPDATE pig_context_checkpoints SET open = CAST('{' AS BLOB)")],
    ["a header with an open object", (rig) => rig.db.exec("UPDATE pig_context_checkpoints SET open = CAST('{}' AS BLOB)")],
    ["another head marker", (rig) => rig.db.exec("UPDATE pig_context_checkpoints SET head = 3")],
    ["another schema version", (rig) => rig.db.exec("UPDATE durable_schema SET version = 8")],
    ["another derivation", (rig) => rig.db.exec("UPDATE pig_context_checkpoints SET derivation = 'other'")],
    ["another digest", (rig) => rig.db.exec("UPDATE pig_context_checkpoints SET digest = '0000000000000000'")],
    ["a tail entry that is not the one it was written for (a restored or foreign store)", (rig) => rig.db.exec(`UPDATE entries SET record = replace(record, '{"kind"', '{ "kind"') WHERE id = ${rig.tail()}`)],
    ["a tail entry the store does not have", (rig) => rig.db.exec("UPDATE pig_context_checkpoints SET tail = 99999")],
    ["no checkpoint tables", (rig) => { rig.db.exec("DROP TABLE pig_context_segments"); rig.db.exec("DROP TABLE pig_context_checkpoints") }],
  ]
  for (const [name, mutate] of mutations) {
    await t.test(name, () => {
      const { rig, read } = checkpointed()
      mutate(rig)
      const got = read()
      assert.deepEqual(got.messages, expected(rig, 2))
      assert.equal(got.rows, rig.tail(), "every entry is read")
    })
  }
})

test("a checkpoint after the requested tail is not used, and a read through it that needs the entries derives them", () => {
  const { rig, read } = checkpointed()
  const earlier = read(rig.tail() - 40)
  assert.deepEqual(earlier.messages, expected(rig, 2, rig.tail() - 40))
  assert.equal(earlier.rows, rig.tail() - 40)
  // The context a model_context summary lists carries every entry; a checkpoint holds none.
  const view = rig.reader().view(2, 0, rig.tail())
  assert.equal(view.entries.length, rig.tail())
  assert.deepEqual(view.messages, expected(rig, 2))
})

// A resumed range holds only the entries after its checkpoint: a tail storage lacks scans nothing, and a read cut off before the
// newest kept tail derives from storage, not from the entries the resumed range holds.
test("a resumed range fails a tail storage lacks and derives an earlier cutoff from storage", () => {
  const { rig, read } = checkpointed()
  const stored = rig.tail()
  assert.throws(() => rig.reader().messages(2, 0, stored + 5), /tail/)
  for (let i = 60; i < 70; i++) rig.turn(2, i)
  const reader = rig.reader()
  assert.deepEqual(read(rig.tail(), reader).messages, expected(rig, 2))
  for (const at of [stored + 6, stored, stored - 40]) assert.deepEqual(reader.messages(2, 0, at), expected(rig, 2, at), `cut off at ${at}`)
  assert.throws(() => reader.messages(2, 0, rig.tail() + 5), /tail/)
  assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
})

test("an edit or a head marker after the checkpoint is derived from the entries", () => {
  const { rig } = checkpointed()
  const keep = rig.turn(2, 60)
  rig.turn(2, 61)
  const marker = rig.entry(2, "pi.compaction", [{ role: "user", content: [{ type: "text", text: "summary" }], timestamp: 1 }], { head: keep })
  rig.turn(2, 62)
  // The checkpoint was written for no head marker, so a request under the marker derives from the entries.
  rig.entryRows = 0
  assert.deepEqual(rig.reader().messages(2, marker, rig.tail()), readContext(rig.memory, 2).messages)
  assert.ok(rig.entryRows > 0)
  // An edit entry added after the checkpoint changes a message the checkpoint holds.
  const { rig: rig2, read: read2 } = checkpointed()
  const target = 5
  rig2.entry(2, "pi.edit", undefined, { edits: [{ target, action: "replace", messages: [{ role: "user", content: "edited", timestamp: 1 }] }] })
  const got = read2(rig2.tail())
  assert.deepEqual(got.messages, expected(rig2, 2))
  assert.ok(JSON.stringify(got.messages).includes("edited"))
})

// Every request reads a head marker's context whole (its summary lists the active entries to the core), so the reader writes
// no checkpoint under a marker: one would never be resumed and would rewrite the messages at every release. One a store holds
// under a marker still resumes a messages read under that marker only, and the summary still lists every active entry.
test("a context under a head marker is not checkpointed, and a checkpoint stored under one resumes under it only", () => {
  const rig = new Rig()
  rig.conversation(2)
  let keep = 0
  for (let i = 0; i < 80; i++) { const first = rig.turn(2, i); if (i === 40) keep = first }
  const marker = rig.entry(2, "pi.compaction", [{ role: "user", content: [{ type: "text", text: "summary" }], timestamp: 1 }], { head: keep })
  for (let i = 80; i < 100; i++) rig.turn(2, i)
  const writer = rig.reader()
  writer.messages(2, marker, rig.tail())
  rig.turn(2, 100)
  writer.messages(2, marker, rig.tail())
  writer.drop(2)
  assert.equal(rig.count("pig_context_checkpoints"), 0)
  const whole = rig.reader(false).read(2, marker, rig.tail())
  rig.segments().save(2, marker, rig.tail(), whole.settled, whole.open, whole.baseline)
  for (let i = 101; i < 104; i++) rig.turn(2, i)
  rig.entryRows = 0
  const reader = rig.reader()
  const got = reader.messages(2, marker, rig.tail())
  assert.deepEqual(got, readContext(rig.memory, 2).messages)
  assert.equal(rig.entryRows, 12)
  const summary = JSON.parse(new TextDecoder().decode(answerContextRead(reader, CONTEXT_SUMMARY, [2, rig.tail(), marker, keep, 0])![0]![0] as Uint8Array)) as { entries: [number, string][] }
  assert.deepEqual(summary.entries, readContext(rig.memory, 2).entries.map((entry) => [entry.id, entry.kind]))
  // The checkpoint is for this marker only: a read with no marker derives from the entries.
  const derived = rig.reader(false).messages(2, 0, rig.tail())
  rig.entryRows = 0
  assert.deepEqual(rig.reader().messages(2, 0, rig.tail()), derived)
  assert.equal(rig.entryRows, rig.tail())
})

test("a checkpoint is written in segments of at most 512 KB and appended to, never rewritten, while the conversation grows", () => {
  const rig = new Rig()
  rig.conversation(2)
  const reader = rig.reader()
  for (let i = 0; i < 60; i++) rig.turn(2, i, 100_000)
  reader.messages(2, 0, rig.tail())
  const sizes = () => (rig.db.prepare("SELECT length(messages) AS n FROM pig_context_segments ORDER BY idx").all() as { n: number }[]).map((r) => Number(r.n))
  const first = sizes()
  assert.ok(first.length >= 11, `${first.length} segments for 6 MB of messages`)
  for (const n of first) assert.ok(n <= SEGMENT_BYTES, `a segment of ${n} bytes`)
  const head = rig.db.prepare("SELECT messages FROM pig_context_segments WHERE idx = 0").get() as { messages: Uint8Array }
  for (let i = 60; i < 70; i++) rig.turn(2, i, 100_000)
  reader.messages(2, 0, rig.tail())
  const second = sizes()
  assert.deepEqual(second.slice(0, first.length - 1), first.slice(0, first.length - 1))
  assert.ok(second.length > first.length)
  assert.deepEqual((rig.db.prepare("SELECT messages FROM pig_context_segments WHERE idx = 0").get() as { messages: Uint8Array }).messages, head.messages)
  assert.deepEqual(rig.reader().messages(2, 0, rig.tail()), expected(rig, 2))
})

test("a checkpoint write that fails leaves nothing behind and the read answers", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 40; i++) rig.turn(2, i)
  // A crash between the segments and the header the commit point: the store sees none of the write.
  rig.failOn = /INSERT OR REPLACE INTO pig_context_checkpoints/
  const reader = rig.reader()
  assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
  assert.equal(rig.count("pig_context_segments"), 0)
  assert.equal(rig.count("pig_context_checkpoints"), 0)
  rig.failOn = undefined
  rig.turn(2, 40)
  assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
  assert.equal(rig.count("pig_context_checkpoints"), 1)
  assert.deepEqual(rig.reader().messages(2, 0, rig.tail()), expected(rig, 2))
})

test("a store with no checkpoint tables, as Pi writes it, is read from the entries, and pi_ tables are not checkpoints", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 40; i++) rig.turn(2, i)
  // The names a harness gives Pi's own tables must not be taken for the checkpoint's.
  rig.db.exec("CREATE TABLE pi_context_checkpoints (conversation INTEGER PRIMARY KEY, head INTEGER, tail INTEGER)")
  rig.db.exec("CREATE TABLE pi_context_segments (conversation INTEGER, idx INTEGER, messages BLOB)")
  rig.db.exec("INSERT INTO pi_context_checkpoints VALUES (2, 0, 1)")
  assert.deepEqual(new ContextReader(rig.all, new ContextSegments({ all: rig.all, run: rig.run, tx: rig.tx })).messages(2, 0, rig.tail()), expected(rig, 2))
  assert.deepEqual(rig.reader(false).messages(2, 0, rig.tail()), expected(rig, 2))
  assert.equal(rig.count("pi_context_checkpoints"), 1)
  // A store that refuses the checkpoint tables (a read-only one) still answers.
  const refused = new Rig()
  refused.conversation(2)
  for (let i = 0; i < 40; i++) refused.turn(2, i)
  refused.failOn = /CREATE TABLE/
  assert.deepEqual(refused.reader().messages(2, 0, refused.tail()), expected(refused, 2))
})

test("a fork reads its parent's checkpoint only under its own conversation", () => {
  const rig = new Rig()
  rig.conversation(2)
  let forkAt = 0
  for (let i = 0; i < 50; i++) { rig.turn(2, i); if (i === 30) forkAt = rig.tail() }
  rig.reader().messages(2, 0, rig.tail())
  rig.conversation(3, { conversationId: 2, at: forkAt })
  rig.turn(3, 900)
  assert.deepEqual(rig.reader().messages(3, 0, rig.tail()), expected(rig, 3))
  assert.deepEqual(rig.reader().messages(2, 0, rig.memory.newest(2)!), expected(rig, 2))
})

// The corpus pi-durable main wrote: after every query a checkpoint is written, and a fresh reader that resumes from it
// derives the messages the cold derivation does, with and without checkpoints.
const corpusDir = join(import.meta.dirname, "../../../history/testdata")
for (const file of ["context.hex", "real.hex", "real104.hex"]) {
  test(`${file}: contexts read through checkpoints equal the derivation from the entries`, () => {
    const rig = new Rig()
    const source = rig.memory
    const writer = rig.reader()
    let queries = 0, resumed = 0, extensions = 0
    for (const line of readFileSync(join(corpusDir, file), "utf8").split("\n")) {
      const f = line.split(" ")
      if (f[0] === "C") {
        const rec = JSON.parse(unhex(f[1]!)) as Conv
        rig.db.prepare("INSERT INTO conversations (id, record) VALUES (?, ?)").run(rec.id, JSON.stringify(rec))
        source.convs.set(rec.id, rec)
      } else if (f[0] === "E") {
        const rec = JSON.parse(unhex(f[3]!)) as EntryRecord
        rig.db.prepare("INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, ?, ?)").run(rec.id, Number(f[1]), (rec.head as number | undefined) ?? null, rec.id, JSON.stringify(rec))
        const list = source.own.get(Number(f[1])) ?? []
        list.push(rec)
        source.own.set(Number(f[1]), list)
      } else if (f[0] === "Q" && !f[3]!.startsWith("!")) {
        queries++
        const conv = Number(f[1]), at = Number(f[2])
        const { view } = readContextFrom(source, conv, at === 0 ? undefined : at, undefined)
        const tail = at === 0 ? source.newest(conv)! : at
        const head = view.head?.id ?? 0
        const want = JSON.stringify(view.messages)
        const key = (m: unknown) => createHash("sha256").update(JSON.stringify(m)).digest("hex")
        // Before the writer's read: a fresh reader resumes from a checkpoint an earlier query wrote and extends it to this tail.
        const extended = rig.checkpointReads
        assert.equal(key(rig.reader().messages(conv, head, tail)), key(JSON.parse(want)), `extended Q ${conv} ${at}`)
        extensions += rig.checkpointReads - extended
        assert.equal(key(writer.messages(conv, head, tail)), key(JSON.parse(want)), `writer Q ${conv} ${at}`)
        const before = rig.checkpointReads
        assert.equal(key(rig.reader().messages(conv, head, tail)), key(JSON.parse(want)), `resumed Q ${conv} ${at}`)
        resumed += rig.checkpointReads - before
        assert.equal(key(rig.reader(false).messages(conv, head, tail)), key(JSON.parse(want)), `without checkpoints Q ${conv} ${at}`)
      }
    }
    assert.ok(queries >= 10, `${queries} queries`)
    assert.ok(resumed > 0, `${resumed} checkpoints resumed`)
    assert.ok(extensions > 0, `${extensions} checkpoints extended`)
  })
}

// An edit can target an entry after its own (edit targets are the caller's and are not checked against the entries). Pi keeps the
// targets in the range (context.ts `edited`), so the target, when it is appended, derives the range again with the edit applied. A
// checkpoint between the edit and its target must not lose that: the cold read would contribute the target unedited.
test("an edit stored before a checkpoint applies to its target appended after it", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 20; i++) rig.turn(2, i)
  rig.reader().messages(2, 0, rig.tail())
  const stored = rig.tail()
  const target = rig.tail() + 7
  rig.entry(2, "pi.edit", undefined, { edits: [{ target, action: "replace", messages: [{ role: "user", content: "edited", timestamp: 1 }] }] })
  rig.turn(2, 20)
  assert.deepEqual(rig.reader().messages(2, 0, rig.tail()), expected(rig, 2))
  const checkpointTail = () => (rig.db.prepare("SELECT tail FROM pig_context_checkpoints").get() as { tail: number }).tail
  assert.equal(checkpointTail(), stored, "no checkpoint between the edit and its target")
  rig.entry(2, "pi.user", [{ role: "user", content: "an entry before", timestamp: 2 }])
  assert.equal(rig.entry(2, "pi.user", [{ role: "user", content: "unedited target", timestamp: 3 }]), target)
  const want = expected(rig, 2)
  assert.ok(JSON.stringify(want).includes("edited") && !JSON.stringify(want).includes("unedited target"))
  assert.deepEqual(rig.reader().messages(2, 0, rig.tail()), want)
  assert.equal(checkpointTail(), target, "the target applied, the range is checkpointed again")
})

// A checkpoint is a cache: one whose messages are well-formed JSON but not messages must be ignored like any other bad one, not
// fail the read (a failed context read is fatal to the handle).
test("a checkpoint whose segments or open list hold something other than messages is ignored", async (t) => {
  const cases: [string, (rig: Rig) => void][] = [
    ["a null settled message", (rig) => rig.db.exec("UPDATE pig_context_segments SET messages = CAST('[' || substr(CAST(messages AS TEXT), 2, length(messages) - 2) || ',null]' AS BLOB), count = count + 1 WHERE idx = 0; UPDATE pig_context_checkpoints SET settled = settled + 1")],
    ["an open assistant message whose content is not a list", (rig) => rig.db.exec(`UPDATE pig_context_checkpoints SET open = CAST('[{"role":"assistant","content":"text","stopReason":"stop"},{"role":"user","content":"u"}]' AS BLOB)`)],
    ["an open object that is not a message", (rig) => rig.db.exec(`UPDATE pig_context_checkpoints SET open = CAST('[{"content":"no role"}]' AS BLOB)`)],
    ["a null open message", (rig) => rig.db.exec("UPDATE pig_context_checkpoints SET open = CAST('[null]' AS BLOB)")],
  ]
  for (const [name, mutate] of cases) {
    await t.test(name, () => {
      const { rig } = checkpointed()
      mutate(rig)
      rig.turn(2, 60)
      assert.deepEqual(rig.reader().messages(2, 0, rig.tail()), expected(rig, 2))
    })
  }
})

// With the production cadence (a checkpoint once 256 messages settled), the cold read that derives a long context writes nothing:
// the checkpoint is written when the core releases the range (pig_context_release), or when the reader evicts it.
test("a due checkpoint is written when the range is dropped or evicted, not by the read that derived it", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 100; i++) rig.turn(2, i)
  const reader = new ContextReader(rig.all, rig.segments())
  reader.messages(2, 0, rig.tail())
  assert.equal(rig.count("pig_context_checkpoints"), 0)
  reader.drop(2)
  assert.equal(rig.count("pig_context_checkpoints"), 1)
  rig.conversation(3)
  for (let i = 0; i < 100; i++) rig.turn(3, i)
  reader.messages(3, 0, rig.tail())
  for (let conv = 100; conv < 100 + 63; conv++) { rig.conversation(conv); rig.turn(conv, 0); reader.messages(conv, 0, rig.tail()) }
  assert.equal(rig.count("pig_context_checkpoints"), 1)
  rig.conversation(200)
  rig.turn(200, 0)
  reader.messages(200, 0, rig.tail())
  assert.equal(rig.count("pig_context_checkpoints"), 2, "conversation 3 evicted")
  rig.entryRows = 0
  assert.deepEqual(rig.reader().messages(3, 0, rig.memory.newest(3)!), expected(rig, 3))
  assert.equal(rig.entryRows, 0)
})

// What a reader remembers of the checkpoints it stored or loaded holds the conversation's whole settled list; a conversation
// the reader drops (the core's release after contextRetentionMs) or evicts (MAX_KEPT_RANGES) must not stay reachable through it.
test("a dropped or evicted conversation's settled messages are not kept by the checkpoint bookkeeping", async () => {
  const { setFlagsFromString } = await import("node:v8")
  const { runInNewContext } = await import("node:vm")
  setFlagsFromString("--expose-gc")
  const gc = runInNewContext("gc") as () => void
  const rig = new Rig()
  for (const conv of [2, 3]) { rig.conversation(conv); for (let i = 0; i < 10; i++) rig.turn(conv, i) }
  const reader = rig.reader()
  const settled = (conv: number) => new WeakRef(reader.read(conv, 0, rig.memory.newest(conv)!).settled)
  const dropped = settled(2)
  reader.messages(2, 0, rig.memory.newest(2)!) // a second read writes the checkpoint (every read after one)
  reader.drop(2)
  const evicted = settled(3)
  reader.messages(3, 0, rig.memory.newest(3)!)
  for (let conv = 100; conv < 100 + 64; conv++) { rig.conversation(conv); rig.turn(conv, 0); reader.messages(conv, 0, rig.memory.newest(conv)!) }
  await new Promise((resolve) => setImmediate(resolve))
  gc()
  assert.equal(dropped.deref(), undefined, "dropped conversation")
  assert.equal(evicted.deref(), undefined, "evicted conversation")
})

test("an edit before the checkpoint that targets an entry after it is applied when that entry arrives", () => {
  // Pi's extendRange derives the range again when an added entry is the target of an earlier edit (previous.edited): an
  // edit may name an id the store has not assigned yet. A checkpoint must carry those targets, or the resumed range
  // contributes the entry unedited.
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 20; i++) rig.turn(2, i)
  const future = rig.tail() + 2
  rig.entry(2, "pi.edit", undefined, { edits: [{ target: future, action: "replace", messages: [{ role: "user", content: "edited", timestamp: 1 }] }] })
  rig.turn(2, 20)
  rig.reader().messages(2, 0, future - 1)
  assert.equal(rig.count("pig_context_checkpoints"), 0, "a range with an edit of an entry after its tail is not checkpointed")
  rig.turn(2, 21)
  const got = rig.reader().messages(2, 0, rig.tail())
  assert.deepEqual(got, expected(rig, 2))
  assert.ok(JSON.stringify(got).includes("edited"))
})

test("a reader holding a resumed range derives an earlier tail and a full view from the entries", () => {
  const { rig } = checkpointed()
  for (let i = 60; i < 62; i++) rig.turn(2, i)
  const reader = rig.reader()
  assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
  // The kept range is the resumed one: it lists only the entries after the checkpoint.
  assert.deepEqual(reader.messages(2, 0, rig.tail() - 20), expected(rig, 2, rig.tail() - 20))
  const view = reader.view(2, 0, rig.tail())
  assert.equal(view.entries.length, rig.tail())
  assert.deepEqual(view.messages, expected(rig, 2))
})

test("a resumed read of a tail storage lacks fails", () => {
  const { rig } = checkpointed()
  assert.throws(() => rig.reader().messages(2, 0, rig.tail() + 5), /does not have/)
})

test("a summary under a head marker lists every active entry with a checkpoint", () => {
  const rig = new Rig()
  rig.conversation(2)
  let keep = 0
  for (let i = 0; i < 40; i++) { const first = rig.turn(2, i); if (i === 20) keep = first }
  const marker = rig.entry(2, "pi.compaction", [{ role: "user", content: [{ type: "text", text: "summary" }], timestamp: 1 }], { head: keep })
  for (let i = 40; i < 60; i++) rig.turn(2, i)
  // A reader writes no checkpoint under a marker; a store can hold one (another writer's) and the summary must still list the entries.
  const through = readContextFrom(rig.memory, 2, rig.tail(), undefined).range!
  rig.segments().save(2, marker, rig.tail(), through.settled, through.open, through.baseline)
  assert.equal(rig.count("pig_context_checkpoints"), 1)
  rig.turn(2, 60)
  const summary = (reader: ContextReader) => {
    const rows = answerContextRead(reader, CONTEXT_SUMMARY, [2, rig.tail(), marker, keep, 0])!
    return JSON.parse(new TextDecoder().decode(rows[0]![0] as Uint8Array))
  }
  assert.deepEqual(summary(rig.reader()), summary(rig.reader(false)))
})

test("a checkpoint whose open messages cannot be ordered is not used", () => {
  const { rig, read } = checkpointed()
  rig.db.exec(`UPDATE pig_context_checkpoints SET open = CAST('[{"role":"assistant","content":5}]' AS BLOB)`)
  assert.deepEqual(read().messages, expected(rig, 2))
})

test("a reader does not append to a checkpoint another writer replaced", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 30; i++) rig.turn(2, i)
  const a = rig.reader()
  a.messages(2, 0, rig.tail())
  // Another reader over the same store writes the checkpoint of an earlier tail, from scratch.
  rig.reader().messages(2, 0, rig.tail() - 40)
  for (let i = 30; i < 32; i++) rig.turn(2, i)
  a.messages(2, 0, rig.tail())
  // The other writer's checkpoint stands whole: its segments are the ones its header counts.
  const segments = rig.db.prepare("SELECT count(*) AS n, sum(count) AS m FROM pig_context_segments").get() as { n: number; m: number }
  const header = rig.db.prepare("SELECT segments, settled FROM pig_context_checkpoints").get() as { segments: number; settled: number }
  assert.deepEqual([Number(segments.n), Number(segments.m)], [Number(header.segments), Number(header.settled)])
  for (let i = 32; i < 34; i++) rig.turn(2, i)
  assert.deepEqual(rig.reader().messages(2, 0, rig.tail()), expected(rig, 2))
})
