// The cost of the model_context reads of a warm turn as the history grows: 9 ContextReader.messages reads after each
// appended turn, at H=500 and H=5,000 turns, in milliseconds per turn. Run: node bench/context-reads-scaling.ts
import { DatabaseSync } from "node:sqlite"
import { ContextReader } from "../src/contextstore.ts"
import type { EntryRecord } from "../src/context.ts"
const enc = new TextEncoder()
class Rig {
  readonly db = new DatabaseSync(":memory:")
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
  }
  entry(conv: number, kind: string, model: unknown[] | undefined, extra: Record<string, unknown> = {}): number {
    const id = this.next++
    const rec = { kind, ...(model ? { model } : {}), id, conversationId: conv, ...extra } as EntryRecord
    this.db.prepare("INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, ?, ?)").run(id, conv, (extra.head as number | undefined) ?? null, id, JSON.stringify(rec))
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

function measure(h: number) {
  const rig = new Rig(); rig.conversation(2)
  for (let i = 0; i < h; i++) rig.turn(2, i)
  const reader = new ContextReader(rig.all)
  reader.messages(2, 0, rig.tail())
  const reps = 200
  let total = 0n
  for (let r = 0; r < reps; r++) {
    rig.turn(2, h + r)
    const tail = rig.tail()
    const t0 = process.hrtime.bigint()
    for (let c = 0; c < 9; c++) reader.messages(2, 0, tail)
    total += process.hrtime.bigint() - t0
  }
  return Number(total) / reps / 1e6
}
for (const h of [500, 5000]) console.log(`H=${h} ms per turn of 9 model_context reads: ${measure(h).toFixed(3)}`)
