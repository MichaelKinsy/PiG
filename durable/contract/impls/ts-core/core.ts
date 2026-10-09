// The synchronous Durable core for the durable-bench workload, in TypeScript (bake-off lane L6, the control).
//
// Same design as the Go core in bench/durable/wasm-spike/core: an event goes in, a batch of commits and one effect
// come out, and nothing here performs I/O, reads a clock or draws randomness. The transcript stays as the stored
// record strings plus a fixed-width message index (role, prompt characters, planned tool count); the warm path never
// decodes a record. A cold open builds the index from the stored rows (or loads it from the sidecar). Records are
// built by templates around JSON.stringify, so number formatting, escaping and key order are the ones Pi's
// JSON.stringify produces.
import { messageChars, ROLE_SYSTEM, ROLE_TOOL_RESULT, ROLE_USER, ROLE_ASSISTANT, parsePlan } from "./faux.ts"
import { modelMessages } from "./splice.ts"

export type Param = string | number | null | Uint8Array
export interface Stmt { sql: number; params: Param[] }
export type Commit = Stmt[]
export const EFFECT_DONE = 0, EFFECT_MODEL = 1, EFFECT_TOOL = 2
/** One step: the commits to apply in order, then one effect (a model request, a tool call `arg`, or done). */
export interface Step { commits: Commit[]; effect: number; arg: number }

// Statement indexes. The host owns the SQL text for each index (engine.ts SQL).
export const SQL_RECORD_ID = 0, SQL_ENTRY = 1, SQL_SUBMISSION = 2, SQL_TASK = 3, SQL_DELETE_REVISIONS = 4, SQL_REVISION = 5, SQL_METADATA = 6, SQL_INDEX_ROW = 7, SQL_INDEX_DELETE = 8

/** Per-turn sidecar chunks merge into one row once this many accumulate. */
const MAX_CHUNKS = 32
/** A sidecar chunk is [role u8][prompt chars u32][plan u16] per message. */
export const INDEX_RECORD = 7
const USAGE_KEY = "faux/scripted-1"
/** Layout identity of the sidecar: a sidecar written under another identity is an invalid cache and is rebuilt (ADR D6). */
export const CORE_ID = "ts-idx-1"

/** FNV-1a over 32-bit lanes, two seeds: a 64-bit hash for cache validation (not a security boundary). */
export class Hash64 {
  lo = 0x811c9dc5; hi = 0x9747b28c
  byte(b: number) { this.lo = Math.imul(this.lo ^ b, 0x01000193); this.hi = Math.imul(this.hi ^ b, 0x01000193) }
  /** Hash the UTF-16 code units of a string. */
  text(s: string) { for (let i = 0; i < s.length; i++) { const c = s.charCodeAt(i); this.byte(c & 0xff); this.byte(c >> 8) } return this }
  /** The hash as 8 bytes. */
  get value() { const b = new Uint8Array(8); const dv = new DataView(b.buffer); dv.setUint32(0, this.lo >>> 0, true); dv.setUint32(4, this.hi >>> 0, true); return b }
}
export const hashText = (s: string) => new Hash64().text(s).value

/** The sidecar's header row payload: what the validation reads without touching the chunks. */
export interface SidecarHeader {
  /** Hash of the whole index (every chunk concatenated). */
  index: Uint8Array
  /** The last covered entry: commit sequence, text length and hash of its record. */
  lastSeq: number; lastLen: number; lastHash: Uint8Array
}
const HEADER_SIZE = 8 + 8 + 8 + 4 + 8
function encodeHeader(h: SidecarHeader) {
  const b = new Uint8Array(HEADER_SIZE), dv = new DataView(b.buffer)
  for (let i = 0; i < 8; i++) b[i] = CORE_ID.charCodeAt(i)
  b.set(h.index, 8); dv.setFloat64(16, h.lastSeq, true); dv.setUint32(24, h.lastLen, true); b.set(h.lastHash, 28)
  return b
}
/** The header of a sidecar written by this core layout, or undefined (another layout, or not a header). */
export function parseHeader(b: Uint8Array | undefined): SidecarHeader | undefined {
  if (!b || b.length !== HEADER_SIZE) return undefined
  for (let i = 0; i < 8; i++) if (b[i] !== CORE_ID.charCodeAt(i)) return undefined
  const dv = new DataView(b.buffer, b.byteOffset, b.byteLength)
  return { index: b.slice(8, 16), lastSeq: dv.getFloat64(16, true), lastLen: dv.getUint32(24, true), lastHash: b.slice(28, 36) }
}

interface Usage { input: number; output: number; cacheRead: number; cacheWrite: number; total: number }
/** A stored record as the object Pi mutates. */
type Rec = Record<string, any>
interface Turn { active: boolean; reqId: string; user: number; sub: number; gen: number; asst: number; toolTask: number; call: number }

/**
 * The record shape to write: the pinned pi-durable release ("1.0.4"), or Pi main at da866ada, whose task records carry
 * Session-stamped `startedAt`/`endedAt` and whose assistant and tool-result messages carry `durationMs`.
 */
export type Shape = "1.0.4" | "da866ada"

export interface Config {
  shape?: Shape
  api: string; conv: number; liveDoc: number; usageDoc: number
  /** durable_metadata.next_id, stored as TEXT. */
  nextId: string; nextSeq: number; lastEntry: number
  /** commit_seq of the last entry (for the sidecar header). */
  lastEntrySeq?: number
  /** The pi.usage document's latest base. */
  usage: string
  /** The pi.live document: its latest base and the deltas after it, in order. */
  live: { base: string; deltas: string[] }
  sidecar: boolean
}

/** What the host reads from a store whose turn was interrupted: the raw records of the live tasks and placed submissions, in id order. */
export interface RecoverInput { tasks: string[]; submissions: string[] }

const INTERRUPTED = "Tool lookup was interrupted and may have partially run"
const ATTEMPT_DELTA = `[["s",["generation"],{"attempt":1}]]`
const TOOL_RUNNING_DELTA = `[["s",["tools",0,"status"],"running"]]`
const ceil4 = (n: number) => Math.ceil(n / 4)

export class CoreError extends Error {}

export class Core {
  private api = ""
  private conv = 0
  private liveDoc = 0
  private usageDoc = 0
  private nextId = 0
  private nextSeq = 0
  private usage: Rec = { models: {}, tools: {} }
  private live: Rec = {}
  private tasks = new Map<number, Rec>()
  private sub: Rec = {}
  private lastEntry = 0
  private lastEntrySeq = 0

  // The index: one slot per model message.
  private roles = new Uint8Array(1024)
  private chars = new Uint32Array(1024)
  private plan = new Uint16Array(1024)
  private n = 0
  private total = 0 // sum of chars
  private idxHash = new Hash64() // running hash of the index bytes, for the sidecar header

  // The arena: stored record strings, in entry order, for the entries this core holds. After a sidecar load only
  // the entries appended since are held until adoptHistory.
  private records: string[] = []
  private held = true
  /** Records already sent to the host's model context. */
  private sent = 0

  // The faux provider's prompt cache for the session: the previous prompt length, absent after a cold start.
  private prevChars = 0
  private hasPrev = false

  private head = false
  private now = 0

  private sidecar = false
  private persisted = 0
  private chunks = 0

  private t: Turn = { active: false, reqId: "", user: 0, sub: 0, gen: 0, asst: 0, toolTask: 0, call: 0 }
  private commits: Commit[] = []
  private cur: Stmt[] = []

  /** JSON.parse calls and characters the core has spent (cold open and recovery only; the warm path adds none). */
  readonly parsed = { calls: 0, chars: 0 }

  get messages() { return this.n }
  get nextSequence() { return this.nextSeq }
  get promptChars() { return this.n === 0 ? 0 : this.total + 2 * (this.n - 1) }

  configure(c: Config) {
    this.usage = JSON.parse(c.usage) as Rec
    this.live = JSON.parse(c.live.base) as Rec
    for (const d of c.live.deltas) for (const op of JSON.parse(d) as unknown[][]) applySet(this.live, op)
    this.tasks.clear()
    this.head = c.shape === "da866ada"
    this.api = c.api; this.conv = c.conv; this.liveDoc = c.liveDoc; this.usageDoc = c.usageDoc
    this.nextId = Number(c.nextId); this.nextSeq = c.nextSeq; this.lastEntry = c.lastEntry; this.lastEntrySeq = c.lastEntrySeq ?? 0; this.sidecar = c.sidecar
    this.n = 0; this.total = 0; this.idxHash = new Hash64(); this.records = []; this.held = true; this.sent = 0
    this.prevChars = 0; this.hasPrev = false; this.persisted = 0; this.chunks = 0
    this.t = { active: false, reqId: "", user: 0, sub: 0, gen: 0, asst: 0, toolTask: 0, call: 0 }
  }

  // --- index

  private addMessage(role: number, chars: number, plan: number) {
    if (this.n === this.roles.length) {
      const grow = <T extends Uint8Array | Uint16Array | Uint32Array>(a: T) => { const b = new (a.constructor as new (n: number) => T)(a.length * 2); b.set(a); return b }
      this.roles = grow(this.roles); this.chars = grow(this.chars); this.plan = grow(this.plan)
    }
    this.roles[this.n] = role; this.chars[this.n] = chars; this.plan[this.n] = plan
    this.n++
    this.total += chars
    const h = this.idxHash
    h.byte(role); h.byte(chars & 0xff); h.byte((chars >>> 8) & 0xff); h.byte((chars >>> 16) & 0xff); h.byte(chars >>> 24); h.byte(plan & 0xff); h.byte(plan >>> 8)
  }

  /** Cold open without a sidecar: build the index from the conversation's stored record strings, in entry order. */
  scan(records: string[]) {
    this.n = 0; this.total = 0; this.idxHash = new Hash64()
    this.indexRecords(records)
    this.records = records; this.held = true; this.sent = 0
    this.persisted = 0; this.chunks = 0
  }

  private indexRecords(records: string[]) {
    for (const r of records) {
      this.parsed.calls++; this.parsed.chars += r.length
      const model = (JSON.parse(r) as { model?: unknown[] }).model
      if (model) for (const m of model) { const x = messageChars(m); this.addMessage(x.role, x.chars, x.plan) }
    }
  }

  /** Index entries committed after the ones a sidecar covers (the prefix path of ADR D6): the delta rows, in entry order. */
  extend(records: string[]) {
    this.indexRecords(records)
    this.records.push(...records)
  }

  /**
   * Cold open from the sidecar: the chunks concatenated in id order. The records stay unread until adoptHistory. The index
   * must match its header's hash and its message count, or the sidecar is not used.
   */
  loadIndex(b: Uint8Array, count: number, chunks: number, header: SidecarHeader): boolean {
    if (b.length !== count * INDEX_RECORD) return false
    this.n = 0; this.total = 0; this.idxHash = new Hash64()
    const dv = new DataView(b.buffer, b.byteOffset, b.byteLength)
    for (let i = 0; i < count; i++) this.addMessage(b[i * INDEX_RECORD]!, dv.getUint32(i * INDEX_RECORD + 1, true), dv.getUint16(i * INDEX_RECORD + 5, true))
    const want = this.idxHash.value
    if (want.some((v, i) => v !== header.index[i])) { this.n = 0; this.total = 0; return false }
    this.records = []; this.held = false; this.sent = 0
    this.persisted = this.n; this.chunks = chunks
    return true
  }

  /** The stored records older than this core's own, read lazily because the host's model needs them. */
  adoptHistory(records: string[]) {
    this.records = records.concat(this.records); this.held = true; this.sent = 0
  }
  get historyHeld() { return this.held }

  /** The index as a sidecar chunk. */
  encodeIndex(from: number, to: number) {
    const b = new Uint8Array((to - from) * INDEX_RECORD), dv = new DataView(b.buffer)
    for (let i = from; i < to; i++) { const o = (i - from) * INDEX_RECORD; b[o] = this.roles[i]!; dv.setUint32(o + 1, this.chars[i]!, true); dv.setUint16(o + 5, this.plan[i]!, true) }
    return b
  }

  // --- the model plane

  /** The scripted model over the index, as durable-bench's next(context) answers: a lookup number, or -1 with the lookups done this turn. */
  stubNext(): { call: number; done: number } {
    let last = -1, total = 0, d = 0
    for (let i = 0; i < this.n; i++) {
      const r = this.roles[i]!
      if (r === ROLE_USER) { last = i; d = 0 } else if (r === ROLE_TOOL_RESULT) { total++; d++ }
    }
    const planned = last >= 0 ? this.plan[last]! : 0
    return d < planned ? { call: total + 1, done: d } : { call: -1, done: d }
  }

  /**
   * The model context the next request extends: the JSON array text of the messages of the records the host has not been
   * sent, spliced from stored bytes. `full` marks a first delivery (the host holds nothing yet).
   */
  modelContext(): { full: boolean; append: string } {
    if (!this.held) throw new CoreError("history not loaded")
    const full = this.sent === 0
    const out: string[] = []
    for (let i = this.sent; i < this.records.length; i++) for (const m of modelMessages(this.records[i]!)) out.push(m)
    this.sent = this.records.length
    return { full, append: "[" + out.join(",") + "]" }
  }

  /** A provider request body, spliced from the stored records. Equal to JSON.stringify({ messages }) of the parsed messages. */
  body(): string {
    if (!this.held) throw new CoreError("history not loaded")
    const out: string[] = []
    for (const r of this.records) for (const m of modelMessages(r)) out.push(m)
    return `{"messages":[${out.join(",")}]}`
  }

  /** pi-ai's faux provider usage estimate for a response of outputChars characters (the prompt cache is per session, in memory). */
  private fauxUsage(outputChars: number): Usage {
    const l = this.promptChars, prompt = ceil4(l)
    const u: Usage = { input: 0, output: ceil4(outputChars), cacheRead: 0, cacheWrite: 0, total: 0 }
    if (this.hasPrev) {
      u.cacheRead = ceil4(this.prevChars); u.cacheWrite = ceil4(l - this.prevChars); u.input = Math.max(0, prompt - u.cacheRead)
    } else { u.input = prompt; u.cacheWrite = prompt }
    this.prevChars = l; this.hasPrev = true
    u.total = u.input + u.output + u.cacheRead + u.cacheWrite
    return u
  }

  // --- commit builders. Each commit takes the next sequence number and ends with the metadata update, as Pi's does.
  //
  // Records the core rewrites are kept as the objects Pi mutates: a task, the placed submission, and the pi.live and pi.usage
  // documents are parsed once from the store (or created here) and then changed with the same assignments Pi makes, so a field
  // the core does not know is carried, in its position, exactly as Pi's spread and draft mutations carry it (CONTRACT 2.3).

  private alloc() { return this.nextId++ }

  private commit(body: (seq: number) => void, final = false) {
    const seq = this.nextSeq++
    this.cur = []
    body(seq)
    this.cur.push({ sql: SQL_METADATA, params: [String(this.nextId), seq + 1] })
    if (final) this.writeSidecar(seq + 1)
    this.commits.push(this.cur)
  }

  private stmt(sql: number, ...params: Param[]) { this.cur.push({ sql, params }) }
  private recordId(id: number, kind: string) { this.stmt(SQL_RECORD_ID, id, kind) }

  /** The task's record: the stored object for a task this core met, else a new one in Pi's key order. */
  private task(id: number, kind: string, input: object, owner: number): Rec {
    let o = this.tasks.get(id)
    if (!o) {
      o = { id, conversationId: this.conv, kind, version: 1, input }
      if (owner) o.owner = owner
      o.background = false; o.abortRequested = false; o.state = {}
      this.tasks.set(id, o)
    }
    return o
  }

  /** Replace the task's state and write the task. */
  private putTask(o: Rec, state: Rec) {
    o.state = state
    this.writeTask(o)
  }

  private writeTask(o: Rec) {
    const status = o.state.status as string
    if (this.head) this.stamp(o, status)
    this.recordId(o.id, "task")
    this.stmt(SQL_TASK, o.id, this.conv, JSON.stringify(o.kind), status, 0, 0, JSON.stringify(o))
    if (status === "terminal") this.tasks.delete(o.id)
  }

  /**
   * Pi main's Session stamps lifecycle times (Transaction.#stampTimes): `startedAt` on the first change to running, `endedAt`
   * on the change to terminal, both carried over from the replaced record and appended after the record's own keys.
   */
  private stamp(o: Rec, status: string) {
    if (o.startedAt === undefined && status === "running") o.startedAt = this.now
    if (o.endedAt === undefined && status === "terminal") o.endedAt = this.now
  }

  private putEntry(id: number, record: string) {
    this.recordId(id, "entry")
    this.stmt(SQL_ENTRY, id, this.conv, null, this.nextSeq - 1, record)
    this.lastEntry = id; this.lastEntrySeq = this.nextSeq - 1
    this.records.push(record)
  }

  private putRevision(doc: number, seq: number, kind: string, content: string) { this.stmt(SQL_REVISION, doc, seq, kind, 1, content) }
  private putBase(doc: number, seq: number, state: unknown) { this.stmt(SQL_DELETE_REVISIONS, doc); this.putRevision(doc, seq, "base", JSON.stringify(state)) }

  private gen(id: number) { return this.task(id, "pi.generation", {}, 0) }
  private tool() { return this.task(this.t.toolTask, "pi.tool", { assistant: this.t.asst, callId: `call-${this.t.call}` }, this.t.gen) }
  private requestCheckpoint() {
    return { phase: "request", attempt: 1, model: { provider: "faux", modelId: "scripted-1" }, thinkingLevel: "off", streamOptions: {}, cutoff: this.lastEntry }
  }
  private genTerminal(o: Rec, entry: number) { this.putTask(o, { status: "terminal", outcome: { status: "completed", result: { entryId: entry } } }) }

  private begin() { this.commits = [] }
  private finish(effect: number, arg = 0): Step { const commits = this.commits; this.commits = []; return { commits, effect, arg } }

  /** The three commits that take a pending generation to a model request. */
  private runGeneration() {
    const g = this.gen(this.t.gen)
    this.commit(() => this.putTask(g, { status: "running", checkpoint: { phase: "prepare", attempt: 1 } }))
    this.commit(() => this.putTask(g, { status: "running", checkpoint: this.requestCheckpoint() }))
    this.commit(seq => { this.live.generation = { attempt: 1 }; this.putRevision(this.liveDoc, seq, "delta", ATTEMPT_DELTA) })
  }

  /** The input event. Four commits, then the model effect. */
  submit(reqId: string, text: string, now: number): Step {
    this.begin()
    this.now = now
    const t = this.t = { active: true, reqId, user: this.alloc(), sub: this.alloc(), gen: this.alloc(), asst: 0, toolTask: 0, call: 0 }
    const rec = `{"model":[{"role":"user","content":${JSON.stringify(text)},"timestamp":${now}}],"kind":"pi.user","id":${t.user},"conversationId":${this.conv}}`
    this.addMessage(ROLE_USER, 4 + 1 + text.length, parsePlan(text))
    this.sub = { conversationId: this.conv, requestId: reqId, type: "input", status: "placed", entry: t.user, id: t.sub }
    this.commit(seq => {
      this.putEntry(t.user, rec)
      this.putSubmission()
      this.putTask(this.gen(t.gen), { status: "pending", checkpoint: { phase: "prepare", attempt: 1 } })
      this.live.run = { taskId: t.gen, inputs: [t.sub] }
      this.putBase(this.liveDoc, seq, this.live)
    })
    this.runGeneration()
    return this.finish(EFFECT_MODEL)
  }

  private putSubmission() {
    const t = this.t, o = this.sub
    this.recordId(t.sub, "submission")
    this.stmt(SQL_SUBMISSION, t.sub, this.conv, JSON.stringify(t.reqId), o.status as string, JSON.stringify(o))
  }

  /** The model effect's answer: call >= 0 asks for lookup(call); call < 0 answers after `done` lookups. */
  modelResult(call: number, done: number, now: number, durationMs?: number): Step {
    const t = this.t
    if (!t.active) throw new CoreError("no active turn")
    this.begin()
    this.now = now
    let content: string, stop: string, outChars: number
    if (call >= 0) {
      const args = `{"n":${call}}`
      content = `[{"type":"toolCall","id":"call-${call}","name":"lookup","arguments":${args}}]`
      stop = "toolUse"; outChars = "lookup:".length + args.length
    } else {
      const text = `done after ${done} lookups`
      content = `[{"type":"text","text":${JSON.stringify(text)}}]`
      stop = "stop"; outChars = text.length
    }
    const u = this.fauxUsage(outChars)
    this.recordUsage(u)

    t.asst = this.alloc()
    const rec = `{"model":[{"role":"assistant","content":${content},"api":${JSON.stringify(this.api)},"provider":"faux","model":"scripted-1","usage":${usageJSON(u)},"stopReason":"${stop}","timestamp":${now}${this.head && durationMs !== undefined ? `,"durationMs":${durationMs}` : ""}}],"kind":"pi.assistant","id":${t.asst},"conversationId":${this.conv},"byTaskId":${t.gen}}`
    this.addMessage(ROLE_ASSISTANT, "assistant".length + 1 + outChars, 0)

    if (call < 0) {
      this.commit(seq => {
        this.putEntry(t.asst, rec)
        this.sub.status = "done"; this.sub.answer = t.asst
        this.putSubmission()
        this.genTerminal(this.gen(t.gen), t.asst)
        delete this.live.run; delete this.live.generation
        this.putBase(this.liveDoc, seq, this.live)
        this.putBase(this.usageDoc, seq, this.usage)
      }, true)
      t.active = false
      return this.finish(EFFECT_DONE)
    }

    t.call = call
    t.toolTask = this.alloc()
    this.commit(seq => {
      this.putEntry(t.asst, rec)
      this.putTask(this.gen(t.gen), { status: "waiting", checkpoint: { phase: "tools", assistant: t.asst, tools: [t.toolTask], pending: [] }, on: [t.toolTask], policy: "allSettled" })
      this.putTask(this.tool(), { status: "pending", checkpoint: { phase: "call" } })
      delete this.live.generation
      this.live.tools = [{ callId: `call-${call}`, name: "lookup", taskId: t.toolTask, status: "pending" }]
      this.putBase(this.liveDoc, seq, this.live)
      this.putBase(this.usageDoc, seq, this.usage)
    })
    this.commit(() => this.putTask(this.tool(), { status: "running", checkpoint: { phase: "call" } }))
    this.commit(seq => this.executeTool(seq))
    return this.finish(EFFECT_TOOL, call)
  }

  /** The tool task reaches its execute phase, and the live document records it running (a delta). */
  private executeTool(seq: number) {
    this.putTask(this.tool(), { status: "running", checkpoint: { phase: "execute", arguments: { n: this.t.call }, replay: "unsafe" } })
    this.live.tools[0].status = "running"
    this.putRevision(this.liveDoc, seq, "delta", TOOL_RUNNING_DELTA)
  }

  /** Pi's recordUsage (src/harness/usage.ts): a new bucket entry is a copy of the usage, an existing one has it added. */
  private recordUsage(u: Usage) {
    const totals = this.usage.models, add = { input: u.input, output: u.output, cacheRead: u.cacheRead, cacheWrite: u.cacheWrite, totalTokens: u.total, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }
    if (!Object.hasOwn(totals, USAGE_KEY)) { totals[USAGE_KEY] = add; return }
    const total = totals[USAGE_KEY]
    total.input += add.input; total.output += add.output; total.cacheRead += add.cacheRead; total.cacheWrite += add.cacheWrite; total.totalTokens += add.totalTokens
    total.cost.input += add.cost.input; total.cost.output += add.cost.output; total.cost.cacheRead += add.cost.cacheRead; total.cost.cacheWrite += add.cost.cacheWrite; total.cost.total += add.cost.total
  }

  /** The tool effect's result text. Six commits, then the next model effect. */
  toolResult(text: string, now: number, durationMs?: number): Step {
    if (!this.t.active) throw new CoreError("no active turn")
    this.begin()
    this.now = now
    this.settleTool(text, "completed", "", false, now, durationMs)
    this.afterTool(false)
    return this.finish(EFFECT_MODEL)
  }

  /** The tool's result entry, its terminal task and the live document. A failed settlement is the interrupted-tool result Pi writes when it recovers a tool that may have run. */
  private settleTool(text: string, outcome: string, errMessage: string, isError: boolean, now: number, durationMs?: number) {
    const t = this.t
    const res = this.alloc()
    const diag = isError ? `{"severity":"error","code":"interrupted","message":${JSON.stringify(errMessage)}}` : ""
    const rec = `{"model":[{"role":"toolResult","toolCallId":"call-${t.call}","toolName":"lookup","content":[{"type":"text","text":${JSON.stringify(text)}}],"isError":${isError},${this.head && durationMs !== undefined ? `"durationMs":${durationMs},` : ""}"timestamp":${now}}],"data":{"diagnostics":[${diag}]},"kind":"pi.tool-result","id":${res},"conversationId":${this.conv},"byTaskId":${t.toolTask}}`
    this.addMessage(ROLE_TOOL_RESULT, "toolResult".length + 1 + "lookup".length + 1 + text.length, 0)
    const result = { entryId: res }
    const ended = outcome === "failed" ? { status: "failed", error: { message: errMessage }, result } : { status: "completed", result }
    this.commit(seq => {
      this.putEntry(res, rec)
      this.putTask(this.tool(), { status: "terminal", outcome: ended })
      this.live.tools[0].status = "done"; this.live.tools[0].entry = res
      this.putBase(this.liveDoc, seq, this.live)
    })
  }

  /** A generation whose tool has settled goes to its next model request: it wakes, finishes, and the next one runs prepare, request and the attempt delta. A generation persisted as running restarts first. */
  private afterTool(resumed: boolean) {
    const t = this.t
    const g = this.gen(t.gen)
    const running = { status: "running", checkpoint: { phase: "tools", assistant: t.asst, tools: [t.toolTask], pending: [] } }
    if (resumed) this.commit(() => this.putTask(g, { ...running, status: "pending" }))
    this.commit(() => this.putTask(g, running))
    const old = t.gen
    t.gen = this.alloc()
    this.commit(seq => {
      this.putTask(this.gen(t.gen), { status: "pending", checkpoint: { phase: "prepare", attempt: 1 } })
      this.genTerminal(g, t.asst)
      delete this.live.tools; this.live.run.taskId = t.gen
      this.putBase(this.liveDoc, seq, this.live)
    })
    void old
    this.runGeneration()
  }

  // --- sidecar

  /** Appends the turn's messages to the index sidecar in the turn's last commit, so its next_seq equals durable_metadata.next_seq whenever the store is quiescent. */
  private writeSidecar(nextSeq: number) {
    if (!this.sidecar) return
    let from = this.persisted
    if (this.chunks >= MAX_CHUNKS) { this.stmt(SQL_INDEX_DELETE); from = 0; this.chunks = 0 }
    if (this.n > from) { this.chunks++; this.stmt(SQL_INDEX_ROW, this.chunks, 0, 0, 0, this.encodeIndex(from, this.n)) }
    this.persisted = this.n
    const last = this.records.at(-1)
    if (last === undefined) throw new CoreError("no record to anchor the sidecar header")
    this.stmt(SQL_INDEX_ROW, 0, nextSeq, this.lastEntry, this.n, encodeHeader({ index: this.idxHash.value, lastSeq: this.lastEntrySeq, lastLen: last.length, lastHash: hashText(last) }))
  }

  /** One commit that replaces the sidecar with the whole in-memory index. */
  sidecarRebuild(): Step {
    this.begin()
    this.cur = []
    this.chunks = MAX_CHUNKS
    this.writeSidecar(this.nextSeq)
    this.commits.push(this.cur)
    return this.finish(EFFECT_DONE)
  }

  // --- recovery

  /**
   * Resumes a turn from the persisted task states the way pi-durable's recovery does: a running task is re-queued as
   * pending, then each task continues from its checkpoint phase. A tool that reached its execute phase may have run, so
   * it settles as interrupted instead of running again.
   */
  recover(input: RecoverInput, now: number): Step {
    this.begin()
    this.now = now
    let gen: Rec | undefined, tool: Rec | undefined
    for (const raw of input.tasks) {
      this.parsed.calls++; this.parsed.chars += raw.length
      const r = JSON.parse(raw) as Rec
      if (r.kind === "pi.generation") gen = r
      else if (r.kind === "pi.tool") tool = r
      else throw new CoreError("recovery: task kind " + r.kind + " is outside this core's workload")
      this.tasks.set(r.id, r)
    }
    if (!gen) return this.finish(EFFECT_DONE)
    if (input.submissions.length !== 1) throw new CoreError("recovery: expected exactly one placed submission")
    this.parsed.calls++; this.parsed.chars += input.submissions[0]!.length
    this.sub = JSON.parse(input.submissions[0]!) as Rec
    const t = this.t = { active: true, reqId: this.sub.requestId, user: this.sub.entry, sub: this.sub.id, gen: gen.id, asst: 0, toolTask: 0, call: 0 }
    const cp = gen.state.checkpoint ?? {}
    t.asst = cp.assistant ?? 0

    const setStatus = (r: Rec, status: string) => this.commit(() => { r.state.status = status; this.writeTask(r) })
    const reset = (r: Rec) => setStatus(r, "pending")
    const run = (r: Rec) => setStatus(r, "running")

    if (tool) {
      t.toolTask = tool.id; t.asst = tool.input?.assistant ?? 0
      const call = Number((tool.input?.callId ?? "").replace(/^call-/, ""))
      if (!Number.isInteger(call)) throw new CoreError("recovery: tool call id " + tool.input?.callId)
      t.call = call
      if (tool.state.status === "running") reset(tool)
      if (tool.state.checkpoint?.phase === "call") {
        run(tool)
        this.commit(seq => this.executeTool(seq))
        return this.finish(EFFECT_TOOL, call)
      }
      run(tool)
      this.settleTool(`<harness>\n[error] ${INTERRUPTED}\n</harness>`, "failed", INTERRUPTED, true, now)
      this.afterTool(false)
      return this.finish(EFFECT_MODEL)
    }
    if (gen.state.status === "waiting") {
      if (cp.tools?.length !== 1) throw new CoreError("recovery: generation waits on an unexpected tool set")
      t.toolTask = cp.tools[0]!
      this.afterTool(false)
    } else if (cp.phase === "tools") {
      if (cp.tools?.length !== 1) throw new CoreError("recovery: generation checkpoint names an unexpected tool set")
      t.toolTask = cp.tools[0]!
      this.afterTool(gen.state.status === "running")
    } else if (cp.phase === "prepare") {
      if (gen.state.status === "running") reset(gen)
      this.runGeneration()
    } else if (cp.phase === "request") {
      if (gen.state.status === "running") reset(gen)
      run(gen)
      if (this.live.generation === undefined) this.commit(seq => { this.live.generation = { attempt: 1 }; this.putRevision(this.liveDoc, seq, "delta", ATTEMPT_DELTA) })
    } else throw new CoreError("recovery: generation phase " + cp.phase + " is outside this core's workload")
    return this.finish(EFFECT_MODEL)
  }
}

const usageJSON = (u: Usage) => `{"input":${u.input},"output":${u.output},"cacheRead":${u.cacheRead},"cacheWrite":${u.cacheWrite},"totalTokens":${u.total},"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`

/**
 * Apply a chord set operation, the only kind this core's workload writes or reads back: ["s", path, value].
 * A delta of another kind in a store this core opens is outside its workload.
 */
function applySet(doc: Rec, op: unknown[]) {
  if (op[0] !== "s" || !Array.isArray(op[1]) || op[1].length === 0) throw new CoreError("document delta outside this core's workload: " + JSON.stringify(op))
  let at: any = doc
  const path = op[1] as (string | number)[]
  for (const k of path.slice(0, -1)) at = at[k]
  at[path.at(-1)!] = op[2]
}
