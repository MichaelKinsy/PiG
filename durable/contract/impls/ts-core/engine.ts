// Host side of the TypeScript core: applies its commit batches to a synchronous SQL store and answers its effects (the
// scripted model and the lookup tool). It is the wasm-spike shim (bench/durable/wasm-spike/src/engine.ts) with the Wasm
// boundary removed: one implementation for a Durable Object (ctx.storage.sql) and for Node (node:sqlite), so one
// verification harness diffs its writes against pi-durable's.
import { next, payload, type Message, type Step } from "./plan.ts"
import { Core, CoreError, EFFECT_DONE, EFFECT_MODEL, hashText, INDEX_RECORD, parseHeader, type Shape, type SidecarHeader, type Step as CoreStep } from "./core.ts"

export type Param = string | number | null | ArrayBuffer
export interface Store {
  run(query: string, ...params: Param[]): void
  all<T extends Record<string, unknown>>(query: string, ...params: Param[]): T[]
  /** Rows as arrays; BLOB columns arrive as ArrayBuffer or Uint8Array. */
  raw(query: string, ...params: Param[]): Iterable<unknown[]>
  tx(work: () => void): void
  /** Rows the storage reports it read and wrote (Durable Object billing units), when it reports them. */
  rows?: { read: number; written: number }
}

/** core: the scripted model computed by the core over its index (bench plane, labelled); js: durable-bench's next() over the whole parsed transcript on every call, as written; jsincr: the same answers from incrementally maintained state. */
export type Stub = "core" | "js" | "jsincr"
export interface Options { stub: Stub; sidecar: boolean; /** The record shape to write; default "1.0.4". */ shape?: Shape }

// Statement text by index; the order matches core.ts SQL_*.
export const SQL = [
  "INSERT OR IGNORE INTO record_ids (id, record_type) VALUES (?, ?)",
  "INSERT INTO entries (id, conversation_id, head, commit_seq, record) VALUES (?, ?, ?, ?, ?)",
  "INSERT INTO submissions (id, conversation_id, request_id, status, record) VALUES (?, ?, ?, ?, ?)\n\t\t\t\t\t\tON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id,\n\t\t\t\t\t\trequest_id = excluded.request_id, status = excluded.status, record = excluded.record",
  "INSERT INTO tasks (id, conversation_id, kind, status, abort_requested, background, record)\n\t\t\t\t\t\tVALUES (?, ?, ?, ?, ?, ?, ?)\n\t\t\t\t\t\tON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id, kind = excluded.kind,\n\t\t\t\t\t\tstatus = excluded.status, abort_requested = excluded.abort_requested,\n\t\t\t\t\t\tbackground = excluded.background, record = excluded.record",
  "DELETE FROM document_revisions WHERE document_id = ?",
  "INSERT INTO document_revisions (document_id, seq, kind, version, content) VALUES (?, ?, ?, ?, ?)",
  "UPDATE durable_metadata SET next_id = ?, next_seq = ? WHERE singleton = 1",
  "INSERT OR REPLACE INTO pig_index (id, next_seq, last_entry, count, payload) VALUES (?, ?, ?, ?, ?)",
  "DELETE FROM pig_index WHERE id > 0",
]
const SIDECAR_DDL = "CREATE TABLE IF NOT EXISTS pig_index (id INTEGER PRIMARY KEY, next_seq INTEGER NOT NULL, last_entry INTEGER NOT NULL, count INTEGER NOT NULL, payload BLOB NOT NULL) STRICT"

export class CrashError extends Error {}

export interface Counters {
  commits: number; statements: number; coreCalls: number; stubCalls: number
  msCore: number; msApply: number; msStub: number; msOpen: number; msLoad: number; msHistory: number; msInstantiate: number; rowsRead: number; rowsWritten: number
  loadPath: string; sidecarRejected: string
  /** SQL read statements issued (open and cold load included) */
  readStatements: number
  /** JSON.parse calls and characters: the core's (index build, recovery) and the host's (model context) */
  coreParseCalls: number; coreParseChars: number; hostParseCalls: number; hostParseChars: number
}
const zero = (): Counters => ({ commits: 0, statements: 0, coreCalls: 0, stubCalls: 0, msCore: 0, msApply: 0, msStub: 0, msOpen: 0, msLoad: 0, msHistory: 0, msInstantiate: 0, rowsRead: 0, rowsWritten: 0, loadPath: "", sidecarRejected: "", readStatements: 0, coreParseCalls: 0, coreParseChars: 0, hostParseCalls: 0, hostParseChars: 0 })

const now = () => performance.now()

type Convert = { role: "user" | "assistant" | "tool"; text: string; calls?: number[] }

export class Engine {
  private core = new Core()
  private opened = false
  private loaded = false
  private unfinished = 0
  private api = `faux:${Date.now()}:${Math.random().toString(36).slice(2)}`
  private cfg!: { conv: number; liveDoc: number; usageDoc: number; nextId: string; nextSeq: number; usage: string; live: { base: string; deltas: string[] } }
  private side?: { nextSeq: number; count: number; lastEntry: number; header: SidecarHeader } | undefined
  private historyUpTo = 0 // entries up to this id are not held by the core after a sidecar load
  private messages?: any[]       // the host's parsed model context, JS-stub variants only
  private incr = { mapped: [] as Convert[], upto: 0, last: -1, total: 0, done: 0, planned: 0 }
  counters: Counters = zero()
  /** Crash injection (verification only): throw after this many non-empty commits have been applied in total. */
  crashAfter?: number | undefined
  private applied = 0
  /** When set, durable-bench's context fingerprint of every model call is appended (verification only). */
  fingerprints?: string[]
  /** Computes the fingerprint of the parsed model context (verification only). */
  fingerprint?: (messages: any[]) => string
  /** When set, every applied commit is recorded as its statements (verification only). */
  trace?: { q: string; p: Param[] }[][] | undefined

  private store: Store
  readonly options: Options

  constructor(store: Store, options: Options) {
    this.options = options
    // Count the read statements the core's host issues: a warm turn issues none.
    this.store = {
      ...store,
      all: (q, ...p) => { this.counters.readStatements++; return store.all(q, ...p) },
      raw: (q, ...p) => { this.counters.readStatements++; return store.raw(q, ...p) },
    } as Store
    Object.defineProperty(this.store, "rows", { get: () => store.rows })
  }

  /** Read the rows Pi's own open reads. No transcript is read here. */
  open() {
    if (this.opened) return
    const t0 = now()
    const s = this.store
    if (this.options.sidecar) s.run(SIDECAR_DDL)
    const meta = s.all<{ next_id: string; next_seq: number }>("SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1")[0]!
    const docs = s.all<{ id: number; kind: string }>("SELECT id, kind FROM documents")
    const docId = (kind: string) => docs.find(d => d.kind === JSON.stringify(kind))!.id
    const liveDoc = docId("pi.live"), usageDoc = docId("pi.usage")
    const usage = s.all<{ content: string }>("SELECT content FROM document_revisions WHERE document_id = ? AND kind = 'base' ORDER BY seq DESC LIMIT 1", usageDoc)[0]!.content
    const revs = s.all<{ kind: string; content: string }>("SELECT kind, content FROM document_revisions WHERE document_id = ? ORDER BY seq", liveDoc)
    const live = { base: revs.find(r => r.kind === "base")!.content, deltas: revs.filter(r => r.kind === "delta").map(r => r.content) }
    const conv = s.all<{ id: number }>("SELECT id FROM conversations ORDER BY id LIMIT 1")[0]!.id
    const open = s.all<{ n: number }>("SELECT (SELECT count(*) FROM tasks WHERE status IN ('pending', 'running', 'waiting', 'completing')) + (SELECT count(*) FROM submissions WHERE status IN ('queued', 'placed')) AS n")[0]!.n
    this.unfinished = open
    this.cfg = { conv, liveDoc, usageDoc, nextId: meta.next_id, nextSeq: meta.next_seq, usage, live }
    if (this.options.sidecar) {
      for (const r of s.raw("SELECT next_seq, last_entry, count, payload FROM pig_index WHERE id = 0")) {
        const header = parseHeader(r[3] instanceof ArrayBuffer ? new Uint8Array(r[3]) : r[3] as Uint8Array | undefined)
        // A sidecar from the future of this store (restored older, or another store's) is not a prefix of it.
        if (header && (r[0] as number) <= meta.next_seq) this.side = { nextSeq: r[0] as number, lastEntry: r[1] as number, count: r[2] as number, header }
      }
    }
    this.counters.msOpen += now() - t0
    this.opened = true
  }

  private configure(lastEntry: number, lastEntrySeq: number) {
    const c = this.cfg
    this.core.configure({ ...(this.options.shape ? { shape: this.options.shape } : {}), api: this.api, conv: c.conv, liveDoc: c.liveDoc, usageDoc: c.usageDoc, nextId: c.nextId, nextSeq: c.nextSeq, lastEntry, lastEntrySeq, usage: c.usage, live: c.live, sidecar: this.options.sidecar })
  }

  /**
   * Cold open from the sidecar (ADR D6). The fast path: its next_seq equals durable_metadata.next_seq, which open already
   * read. The prefix path: the store has moved on (another conversation, a pi-durable turn, a crash before the sidecar write);
   * the last covered entry is read by primary key and must equal what the header recorded, and the entries after it load as rows.
   * Anything else (another layout, a short or altered chunk, a different last entry) leaves the sidecar unused.
   */
  private loadSidecar(side: NonNullable<Engine["side"]>): boolean {
    const s = this.store, core = this.core, h = side.header
    let delta: string[] = [], lastEntry = side.lastEntry, lastSeq = h.lastSeq
    if (side.nextSeq !== this.cfg.nextSeq) {
      const last = s.all<{ commit_seq: number; record: string }>("SELECT commit_seq, record FROM entries WHERE id = ? AND conversation_id = ?", side.lastEntry, this.cfg.conv)[0]
      if (!last || last.commit_seq !== h.lastSeq || last.record.length !== h.lastLen || hashText(last.record).some((v, i) => v !== h.lastHash[i])) { this.counters.loadPath = "sidecar-stale"; return false }
      for (const row of s.raw("SELECT id, commit_seq, record FROM entries WHERE conversation_id = ? AND id > ? ORDER BY id", this.cfg.conv, side.lastEntry)) { delta.push(row[2] as string); lastEntry = row[0] as number; lastSeq = row[1] as number }
    }
    let total = 0, chunks = 0
    const parts: Uint8Array[] = []
    for (const row of s.raw("SELECT payload FROM pig_index WHERE id > 0 ORDER BY id")) { const b = new Uint8Array(row[0] as ArrayBuffer); parts.push(b); total += b.length; chunks++ }
    const all = new Uint8Array(total)
    let off = 0
    for (const b of parts) { all.set(b, off); off += b.length }
    this.configure(lastEntry, lastSeq)
    if (!core.loadIndex(all, side.count, chunks, h)) { this.counters.loadPath = "sidecar-invalid"; return false }
    this.historyUpTo = side.lastEntry
    if (delta.length) core.extend(delta)
    this.counters.loadPath = delta.length || side.nextSeq !== this.cfg.nextSeq ? "sidecar+delta" : "sidecar"
    return true
  }

  /** Load the transcript index: from a valid sidecar, else from Pi's entry rows. Lands in the first turn, as Pi's read does. */
  private load() {
    if (this.loaded) return
    const t0 = now()
    const s = this.store, core = this.core
    let rejected = ""
    if (this.side && !this.loadSidecar(this.side)) { rejected = this.counters.loadPath; this.side = undefined }
    if (!this.side) {
      // One pass over the rows: each is a billed row read, so the size is not queried separately.
      const records: string[] = []
      let lastEntry = 0, lastSeq = 0
      for (const row of s.raw("SELECT id, commit_seq, record FROM entries WHERE conversation_id = ? ORDER BY id", this.cfg.conv)) { records.push(row[2] as string); lastEntry = row[0] as number; lastSeq = row[1] as number }
      this.configure(lastEntry, lastSeq)
      core.scan(records)
      this.counters.loadPath = "scan"; this.counters.sidecarRejected = rejected
      if (this.options.sidecar) { // the rebuild is not a turn commit
        const armed = this.crashAfter
        this.crashAfter = undefined
        this.applyStep(core.sidecarRebuild())
        this.crashAfter = armed; this.applied = 0
      }
    }
    this.counters.msLoad += now() - t0
    this.loaded = true
  }

  private applyStep(step: CoreStep) {
    const t0 = now()
    for (const commit of step.commits) {
      const group: { q: string; p: Param[] }[] = []
      this.trace?.push(group)
      this.store.tx(() => {
        for (const st of commit) {
          const params = st.params as Param[]
          for (let i = 0; i < params.length; i++) { const p = params[i]; if (p instanceof Uint8Array) params[i] = p.buffer.slice(p.byteOffset, p.byteOffset + p.byteLength) as ArrayBuffer }
          this.store.run(SQL[st.sql]!, ...params)
          if (this.trace) group.push({ q: SQL[st.sql]!, p: params })
        }
      })
      this.counters.commits++; this.counters.statements += commit.length
      if (commit.length && ++this.applied === this.crashAfter) throw new CrashError(`crash after commit ${this.applied}`)
    }
    this.counters.msApply += now() - t0
  }

  private timed<T>(call: () => T): T {
    const t0 = now(); const r = call(); this.counters.msCore += now() - t0; this.counters.coreCalls++; return r
  }

  // durable-bench's scripted model, as written in src/pi.ts: convert the whole transcript, then next().
  private convert(m: any): Convert {
    const blocks: any[] = typeof m.content === "string" ? [{ type: "text", text: m.content }] : m.content
    const text = blocks.filter(b => b.type === "text").map(b => b.text).join("")
    const calls = blocks.filter(b => b.type === "toolCall").map(b => b.arguments.n)
    return { role: m.role === "toolResult" ? "tool" : m.role, text, ...(calls.length ? { calls } : {}) }
  }

  /** The host's model context: the core delivers the messages the host has not seen as spliced stored text (the delta form of ABI 6), after reading the entries a sidecar load left unread. */
  private syncMessages() {
    const core = this.core
    if (!core.historyHeld) {
      const t0 = now()
      const records: string[] = []
      for (const row of this.store.raw("SELECT record FROM entries WHERE conversation_id = ? AND id <= ? ORDER BY id", this.cfg.conv, this.historyUpTo)) records.push(row[0] as string)
      core.adoptHistory(records)
      this.counters.msHistory += now() - t0
    }
    const ctx = core.modelContext()
    this.messages ??= []
    this.counters.hostParseCalls++; this.counters.hostParseChars += ctx.append.length
    for (const m of JSON.parse(ctx.append)) this.messages.push(m)
  }

  private modelStep(): { call: number; done: number; ms: number } {
    const t0 = now()
    let r: { call: number; done: number }
    if (this.fingerprints) { this.syncMessages(); this.fingerprints.push(this.fingerprint!(this.messages!)) }
    if (this.options.stub === "core") r = this.core.stubNext()
    else {
      this.syncMessages()
      let step: Step
      if (this.options.stub === "js") {
        const seen = this.messages!.filter(m => ["user", "assistant", "toolResult"].includes(m.role)).map(m => this.convert(m)) as Message[]
        step = next(seen)
      } else {
        const i = this.incr
        for (; i.upto < this.messages!.length; i.upto++) {
          const m = this.messages![i.upto]
          if (!["user", "assistant", "toolResult"].includes(m.role)) continue
          const c = this.convert(m); i.mapped.push(c)
          if (c.role === "user") { i.last = i.mapped.length - 1; i.done = 0; i.planned = Number(/tools=(\d+)/.exec(c.text)?.[1] ?? 0) }
          else if (c.role === "tool") { i.total++; i.done++ }
        }
        step = i.done < i.planned ? { call: i.total + 1 } : { answer: `done after ${i.done} lookups` }
      }
      r = "call" in step ? { call: step.call, done: 0 } : { call: -1, done: Number(/\d+/.exec(step.answer)![0]) }
    }
    const ms = now() - t0
    this.counters.msStub += ms; this.counters.stubCalls++
    return { ...r, ms: Math.round(ms) }
  }

  /** Finish a turn interrupted by a crash. */
  recover() { this.open(); this.load(); this.finishInterrupted() }

  /** Open and load the index without running a turn (builds the sidecar on a fixture that has none). */
  prepare() { this.open(); this.load() }

  /** Return the counters accumulated since the last call. */
  takeCounters() {
    const c = this.counters, r = this.store.rows
    c.coreParseCalls = this.core.parsed.calls - this.parseSeen.calls; c.coreParseChars = this.core.parsed.chars - this.parseSeen.chars
    this.parseSeen = { ...this.core.parsed }
    if (r) { c.rowsRead = r.read - this.rowsSeen.read; c.rowsWritten = r.written - this.rowsSeen.written; this.rowsSeen = { ...r } }
    this.counters = zero(); this.counters.loadPath = c.loadPath; this.counters.sidecarRejected = c.sidecarRejected
    return c
  }
  private rowsSeen = { read: 0, written: 0 }
  private parseSeen = { calls: 0, chars: 0 }

  private finishInterrupted() {
    const s = this.store
    if (!this.unfinished) return
    const tasks = s.all<{ record: string }>("SELECT record FROM tasks WHERE status IN ('pending', 'running', 'waiting', 'completing') ORDER BY id").map(r => r.record)
    const submissions = s.all<{ record: string }>("SELECT record FROM submissions WHERE status IN ('queued', 'placed') ORDER BY id").map(r => r.record)
    const step = this.timed(() => this.core.recover({ tasks, submissions }, Date.now()))
    this.unfinished = 0
    this.drive(step)
  }

  /** Run one input to completion: submit, then answer each effect until the core reports done. */
  turn(input: { id: string; text: string }) {
    this.open()
    this.load()
    if (this.unfinished) throw new Error("store has an interrupted turn; call recover() first")
    this.drive(this.timed(() => this.core.submit(input.id, input.text, Date.now())))
  }

  /** Apply a step and answer its effects until the core reports done. */
  private drive(step: CoreStep) {
    for (;;) {
      this.applyStep(step)
      if (step.effect === EFFECT_DONE) break
      if (step.effect === EFFECT_MODEL) {
        const r = this.modelStep()
        step = this.timed(() => this.core.modelResult(r.call, r.done, Date.now(), r.ms))
      } else {
        const t0 = now()
        const text = payload(step.arg)
        const ms = Math.round(now() - t0)
        step = this.timed(() => this.core.toolResult(text, Date.now(), ms))
      }
    }
  }

  /** The core's retained message count (index entries). */
  messageCount() { return this.core.messages }
  /** JS heap in use, when the runtime reports it. */
  heapMB() { try { return (globalThis as any).process?.memoryUsage?.().heapUsed / 1048576 || 0 } catch { return 0 } }
}

export { CoreError, INDEX_RECORD }
