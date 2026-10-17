import assert from "node:assert/strict"
import { test } from "node:test"
import { DatabaseSync } from "node:sqlite"
import { readContext, readContextFrom, rangeWithin, type ContextRange, type EntryRecord } from "../src/context.ts"
import { setFlagsFromString } from "node:v8"
import { runInNewContext } from "node:vm"
import { ContextReader } from "../src/contextstore.ts"
import { expandRecipe, RecipeError } from "../src/recipe.ts"
import { MemorySource } from "./memory-source.ts"

const enc = new TextEncoder()

// A store with the tables a range derivation reads, and the same conversations in memory for the corpus-proven derivation.
class Rig {
  readonly db = new DatabaseSync(":memory:")
  readonly memory = new MemorySource()
  rowsRead = 0
  next = 1
  constructor() {
    this.db.exec("CREATE TABLE conversations (id INTEGER PRIMARY KEY, record TEXT NOT NULL)")
    this.db.exec("CREATE TABLE entries (id INTEGER PRIMARY KEY, conversation_id INTEGER NOT NULL, head INTEGER, commit_seq INTEGER NOT NULL, record TEXT NOT NULL)")
  }
  all = (sql: string, params: (number | null)[]): unknown[][] => {
    const stmt = this.db.prepare(sql)
    stmt.setReturnArrays(true)
    const rows = stmt.all(...params) as unknown as unknown[][]
    this.rowsRead += rows.length
    return rows.map((r) => r.map((v) => (typeof v === "string" ? enc.encode(v) : v)))
  }
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
  turn(conv: number, n: number): number {
    const first = this.entry(conv, "pi.user", [{ role: "user", content: `question ${n}`, timestamp: n }])
    this.entry(conv, "pi.assistant", [{ role: "assistant", content: [{ type: "toolCall", id: `call_${n}`, name: "read", arguments: {} }], stopReason: "toolUse", timestamp: n }])
    this.entry(conv, "pi.tool-result", [{ role: "toolResult", toolCallId: `call_${n}`, toolName: "read", content: [{ type: "text", text: `result ${n}` }], isError: false, timestamp: n }])
    this.entry(conv, "pi.assistant", [{ role: "assistant", content: [{ type: "text", text: `answer ${n}` }], stopReason: "stop", timestamp: n }])
    return first
  }
  tail() { return this.next - 1 }
}

const expected = (rig: Rig, conv: number, at?: number) => readContext(rig.memory, conv, at).messages

test("a range derives the context pi derives, and a later request reads only the entries after the ones the reader holds", () => {
  const rig = new Rig()
  rig.conversation(2)
  let keep = 0
  for (let i = 0; i < 20; i++) { const first = rig.turn(2, i); if (i === 15) keep = first }
  const reader = new ContextReader(rig.all)
  assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
  for (let round = 0; round < 3; round++) {
    for (let i = 0; i < 5; i++) rig.turn(2, 100 + round * 5 + i)
    const before = rig.rowsRead
    assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
    assert.ok(rig.rowsRead - before <= 20, `round ${round}: ${rig.rowsRead - before} rows read for 20 new entries`)
  }
  // A head marker starts the context at its head, and a dangling call gets the synthesized result.
  const marker = rig.entry(2, "pi.compaction", [{ role: "user", content: [{ type: "text", text: "summary" }], timestamp: 1 }], { head: keep })
  const under = reader.messages(2, marker, rig.tail())
  assert.deepEqual(under, expected(rig, 2))
  assert.equal((under[0] as unknown as { content: { text: string }[] }).content[0]!.text, "summary")
  rig.entry(2, "pi.user", [{ role: "user", content: "again", timestamp: 50 }])
  rig.entry(2, "pi.assistant", [{ role: "assistant", content: [{ type: "toolCall", id: "orphan", name: "read", arguments: {} }], stopReason: "toolUse", timestamp: 51 }])
  const orphan = reader.messages(2, marker, rig.tail())
  assert.deepEqual(orphan, expected(rig, 2))
  assert.equal((orphan.at(-1) as unknown as { details: { reason: string } }).details.reason, "missing_result")
  // An earlier cutoff is derived again, and a fork sees its parent's entries up to the fork point.
  assert.deepEqual(reader.messages(2, 0, keep + 3), expected(rig, 2, keep + 3))
  rig.conversation(3, { conversationId: 2, at: keep + 1 })
  rig.entry(3, "pi.user", [{ role: "user", content: "fork", timestamp: 60 }])
  const fork = reader.messages(3, 0, rig.tail())
  assert.deepEqual(fork, expected(rig, 3))
  assert.ok(JSON.stringify(fork).includes('"question 15"') && !JSON.stringify(fork).includes('"question 16"') && JSON.stringify(fork).includes('"fork"'))
})

test("a range the store disagrees with is an error", () => {
  const rig = new Rig()
  rig.conversation(2)
  const first = rig.turn(2, 0)
  const reader = new ContextReader(rig.all)
  assert.throws(() => reader.messages(2, 999, rig.tail()), RecipeError)
  assert.throws(() => reader.messages(2, first, rig.tail()), /not a head marker/)
  assert.throws(() => reader.messages(9, 0, rig.tail()), /Unknown conversation/)
  assert.throws(() => reader.messages(2, 0, rig.tail() + 5), RecipeError)
})

test("a model_context range segment expands to null and hands the range to the reader", () => {
  const u32 = (v: number) => [v & 255, (v >>> 8) & 255, (v >>> 16) & 255, (v >>> 24) & 255]
  const text = (s: string) => [0, ...u32(s.length), ...enc.encode(s)]
  const recipe = Uint8Array.from([...u32(3), ...text(`{"context":{"messages":[`), 1, ...u32(7), ...u32(0), ...u32(40), ...text(`]}}`)])
  const seen: number[][] = []
  const out = expandRecipe(recipe, (c, h, t) => { seen.push([c, h, t]) })
  assert.equal(new TextDecoder().decode(out), `{"context":{"messages":[null]}}`)
  assert.deepEqual(seen, [[7, 0, 40]])
  assert.throws(() => expandRecipe(recipe), RecipeError)
  for (const bad of [new Uint8Array(0), recipe.subarray(0, recipe.length - 1), Uint8Array.from([...recipe, 0]), Uint8Array.from([1, 0, 0, 0, 7])]) {
    assert.throws(() => expandRecipe(bad, () => {}), RecipeError)
  }
})

test("a context longer than a page of records is read a page at a time", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 700; i++) rig.turn(2, i)
  assert.ok(rig.tail() > 2048)
  const reader = new ContextReader(rig.all)
  assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
})

// The page statement counts its records: a page of fewer than asked for is the last, so a scan reads no empty page after it. A
// scan of a full last page reads one empty page, and a fork's scan reads its own entries and its parent's, one scan each.
test("a scan ends at a page shorter than the page size, without reading an empty page", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 256; i++) rig.turn(2, i) // 1,024 entries: one full page
  rig.conversation(3, { conversationId: 2, at: 500 })
  rig.turn(3, 1000)
  let pages = 0
  const counting = (sql: string, params: (number | null)[]) => { if (sql.includes("group_concat")) pages++; return rig.all(sql, params) }
  const read = (conv: number, tail: number, reader = new ContextReader(counting)) => {
    pages = 0
    assert.deepEqual(reader.messages(conv, 0, tail), expected(rig, conv, tail))
    return pages
  }
  assert.equal(read(2, 1000), 1, "1,000 entries")
  assert.equal(read(2, 1024), 2, "1,024 entries: a full page, then an empty one")
  assert.equal(read(3, rig.tail()), 2, "a fork: its own page and its parent's")
  const kept = new ContextReader(counting)
  read(2, 1000, kept)
  assert.equal(read(2, 1024, kept), 1, "an extension by 24 entries")
})

test("a fork's context crosses page boundaries of its own conversation and its parent's", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 400; i++) rig.turn(2, i) // 1,600 entries: the fork point lies inside the second page
  const at = 1300
  rig.conversation(3, { conversationId: 2, at })
  for (let i = 0; i < 300; i++) rig.turn(3, 1000 + i)
  const forkTail = rig.tail()
  for (let i = 0; i < 20; i++) rig.turn(2, 2000 + i) // entries of the parent above the fork point are not the fork's
  const parentTail = rig.tail()
  const reader = new ContextReader(rig.all)
  assert.deepEqual(reader.messages(3, 0, forkTail), expected(rig, 3, forkTail))
  assert.deepEqual(reader.messages(2, 0, parentTail), expected(rig, 2, parentTail))
})

test("a page too large for one result value is read again in half the records, and the reader keeps the smaller page", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 100; i++) rig.turn(2, i)
  const limit = 6000 // bytes of one page value, as a Durable Object limits a value
  const pages: number[] = []
  const store = (sql: string, params: (number | null)[]) => {
    const rows = rig.all(sql, params)
    if (sql.includes("group_concat")) {
      pages.push(params[3] as number)
      if (rows[0]![2] !== null && (rows[0]![2] as Uint8Array).length > limit) throw new Error("string or blob too big")
    }
    return rows
  }
  const reader = new ContextReader(store)
  assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
  const failed = pages.filter((n, i) => i > 0 && n < pages[i - 1]!).length
  assert.ok(failed >= 3, `the page size was halved ${failed} times: ${pages.join(",")}`)
  const sizes = pages.slice(pages.lastIndexOf(Math.min(...pages)))
  assert.ok(sizes.every((n) => n === sizes[0]), "a later page does not grow back")
  // A single record that does not fit is an error: the store cannot return it either. The store answers a page of no records
  // (an empty value), so a reader that halved past one record would read a shorter context instead of failing.
  const one = new ContextReader((sql, params) => {
    const rows = rig.all(sql, params)
    if (sql.includes("group_concat") && rows[0]![2] !== null) throw new Error("string or blob too big")
    return rows
  })
  assert.throws(() => one.messages(2, 0, rig.tail()), /too big/)
})

test("a kept range is extended in place, and an extension that fails leaves the kept range as it was", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 30; i++) rig.turn(2, i)
  const reader = new ContextReader(rig.all)
  const first = rig.tail()
  assert.deepEqual(reader.messages(2, 0, first), expected(rig, 2))
  const entries = reader.read(2, 0, first).entries
  for (let i = 30; i < 40; i++) rig.turn(2, i)
  assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
  assert.equal(reader.read(2, 0, rig.tail()).entries, entries, "the extension appended to the kept list")
  // A record whose model is not a list cannot be derived: the extension throws before it changes anything.
  const good = rig.tail()
  rig.db.prepare("INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, 2, NULL, ?, ?)").run(good + 1, good + 1, JSON.stringify({ id: good + 1, conversationId: 2, kind: "pi.user", model: 5 }))
  assert.throws(() => reader.messages(2, 0, good + 1))
  assert.deepEqual(reader.messages(2, 0, good), expected(rig, 2, good))
  assert.equal(reader.read(2, 0, good).entries.length, entries.length, "a failed extension did not append")
})

// The tool results of the open messages are ordered with the extension, as Pi's extendRange orders them, not when the messages are first
// asked for: an assistant message whose content is not a list fails the extension and the kept range stays at its tail.
test("an extension whose open messages cannot be ordered fails before it appends", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 3; i++) rig.turn(2, i)
  const reader = new ContextReader(rig.all)
  const good = rig.tail()
  assert.deepEqual(reader.messages(2, 0, good), expected(rig, 2))
  const entries = reader.read(2, 0, good).entries
  const length = entries.length
  rig.db.prepare("INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, 2, NULL, ?, ?)").run(good + 1, good + 1, JSON.stringify({ id: good + 1, conversationId: 2, kind: "pi.assistant", model: [{ role: "assistant", content: 5, stopReason: "stop", timestamp: 9 }] }))
  assert.throws(() => reader.read(2, 0, good + 1), TypeError)
  assert.equal(entries.length, length, "a failed extension did not append")
  assert.deepEqual(reader.messages(2, 0, good), expected(rig, 2, good))
})

// Pi's extendRange concatenates with an array literal, which takes any number of entries. An extension in place appends as many entries as
// arrived since the kept tail; a spread call (push(...added)) exceeds V8's argument limit near 125,000 and would throw after `settled` grew.
test("a kept range extended in place by more entries than a call takes as arguments equals the range derived at once", () => {
  const source = new MemorySource()
  source.convs.set(2, { id: 2 })
  const own: EntryRecord[] = []
  source.own.set(2, own)
  const add = (role: string, n: number) => {
    const id = own.length + 1
    own.push({ id, conversationId: 2, kind: `pi.${role}`, model: [role === "assistant" ? { role, content: [{ type: "text", text: `a${n}` }], stopReason: "stop", timestamp: n } : { role, content: `u${n}`, timestamp: n }] })
  }
  add("user", 0)
  add("assistant", 0)
  const { range } = readContextFrom(source, 2, undefined, undefined, true)
  for (let i = 1; i <= 75_000; i++) { add("user", i); add("assistant", i) }
  const { view } = readContextFrom(source, 2, undefined, range, true)
  const want = readContext(source, 2)
  assert.equal(view.entries.length, want.entries.length)
  assert.deepEqual(view.messages, want.messages)
})

// The model request's list is the caller's (effects.ts appends to a list it keeps for a later request): changing it does not change the kept range.
test("the messages a request takes are its own, for a derived and for an extended range", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 3; i++) rig.turn(2, i)
  const reader = new ContextReader(rig.all)
  for (let round = 0; round < 2; round++) {
    const taken = reader.messages(2, 0, rig.tail())
    taken.push({ role: "user", content: "appended by the caller", timestamp: 0 })
    taken.reverse()
    assert.deepEqual(reader.messages(2, 0, rig.tail()), expected(rig, 2))
    rig.turn(2, 10 + round)
  }
})

test("a context read is Pi's ContextView: head only when there is one, the records and messages frozen, the arrays the caller's", () => {
  const rig = new Rig()
  rig.conversation(2)
  let keep = 0
  for (let i = 0; i < 6; i++) { const first = rig.turn(2, i); if (i === 3) keep = first }
  const reader = new ContextReader(rig.all)
  const plain = reader.view(2, 0, rig.tail())
  assert.equal("head" in plain, false)
  assert.deepEqual(JSON.parse(JSON.stringify(plain)), JSON.parse(JSON.stringify({ entries: readContext(rig.memory, 2).entries, contributions: readContext(rig.memory, 2).contributions, messages: expected(rig, 2) })))
  assert.ok(Object.isFrozen(plain.messages[0]) && Object.isFrozen(plain.entries[0]) && !Object.isFrozen(plain.messages))
  plain.messages.pop()
  assert.equal(reader.view(2, 0, rig.tail()).messages.length, plain.messages.length + 1, "the caller's array is its own")
  const marker = rig.entry(2, "pi.compaction", [{ role: "user", content: [{ type: "text", text: "summary" }], timestamp: 1 }], { head: keep })
  const marked = reader.view(2, marker, rig.tail())
  assert.equal(marked.head?.id, marker)
  assert.ok(Object.isFrozen(marked.head))
  assert.deepEqual(marked.messages, expected(rig, 2))
})

test("a page whose records group_concat joined out of id order is read in id order", () => {
  const rig = new Rig()
  rig.conversation(2)
  for (let i = 0; i < 300; i++) rig.turn(2, i)
  // A store whose aggregate joins each page's records newest first: SQLite does not specify group_concat's order.
  const reversed = (sql: string, params: (number | null)[]) => {
    const rows = rig.all(sql, params)
    if (!sql.includes("group_concat") || rows[0]![2] === null) return rows
    const records = JSON.parse(new TextDecoder().decode(rows[0]![2] as Uint8Array)) as unknown[]
    return [[rows[0]![0], rows[0]![1], new TextEncoder().encode(JSON.stringify(records.reverse()))]]
  }
  assert.deepEqual(new ContextReader(reversed).messages(2, 0, rig.tail()), expected(rig, 2))
})

// A kept view's lazy messages getter must not hold the range it extended: V8 gives the closures of a function one shared
// context, so a getter made inside extendRange kept `previous`, and every range kept the whole chain of the ranges before it.
test("a kept range does not retain the range it extended", async () => {
  setFlagsFromString("--expose-gc")
  const gc = runInNewContext("gc") as () => void
  const rig = new Rig()
  rig.conversation(2)
  rig.turn(2, 0)
  const scan = (min: number | undefined, max: number) => rig.memory.scan(2, min, max)
  let range: ContextRange = rangeWithin({ head: undefined, tail: rig.tail() }, undefined, scan, true)
  const weak: WeakRef<ContextRange>[] = []
  for (let i = 1; i < 12; i++) {
    weak.push(new WeakRef(range))
    rig.turn(2, i)
    const next = rangeWithin({ head: undefined, tail: rig.tail() }, range, scan, true)
    void next.view.messages
    range = next
  }
  await new Promise((resolve) => setTimeout(resolve, 0))
  gc()
  assert.equal(weak.filter((w) => w.deref() !== undefined).length, 0, "earlier ranges are still reachable from the kept one")
  assert.deepEqual(range.view.messages, expected(rig, 2))
})
