// Change detection for the live Harness settings (harness.ts `syncSettings`): is what `resolveSettings(raw)` would send the same
// as what the core was last sent? Comparing the resolved leaves with the sent copy costs no allocation and no serialisation;
// the JSON text comparison it replaces stringified the whole object before every event.
//
// The fast path answers `true` ONLY when it has proven every sent leaf equal, in the sent order; it answers `false` for anything it
// cannot prove (a value JSON would convert, a class instance, a throwing getter), and the caller then runs the exact text comparison.
// So a wrong `false` costs one stringify and a wrong `true` is the only defect: every branch below returns `false` when unsure.
import { DEFAULT_COMPACTION_POLICY, DEFAULT_CONTEXT_RETENTION_MS, DEFAULT_PROGRESS_POLICY, DEFAULT_RETRY_POLICY, documentRetentionWire } from "./agent.ts"

const hasOwn = Object.prototype.hasOwnProperty
const ownEnumerable = Object.prototype.propertyIsEnumerable

/** A plain object (what `{...x}` and JSON.stringify treat as a record), without a `toJSON`. */
function plain(v: unknown): v is Record<string, unknown> {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return false
  const proto = Object.getPrototypeOf(v)
  return (proto === Object.prototype || proto === null) && typeof (v as { toJSON?: unknown }).toJSON !== "function"
}

/** `value` serialises to exactly the parsed `sent`: strings, booleans, finite numbers, null, and plain objects and arrays of them. */
function same(value: unknown, sent: unknown): boolean {
  switch (typeof value) {
    case "string": case "boolean": return value === sent
    case "number": return Number.isFinite(value) && value === sent
    case "object": break
    default: return false
  }
  if (value === null) return sent === null
  if (Array.isArray(value)) {
    if (!Array.isArray(sent) || sent.length !== value.length) return false
    for (let i = 0; i < value.length; i++) if (!same(value[i], sent[i])) return false
    return true
  }
  if (!plain(value) || !plain(sent)) return false
  let n = 0
  const sentKeys = Object.keys(sent)
  for (const k of Object.keys(value)) {
    const v = value[k]
    if (v === undefined) continue
    if (sentKeys[n++] !== k || !same(v, sent[k])) return false
  }
  return n === sentKeys.length
}

/** The next expected key of a record is `key` with `value` (an undefined value is absent from JSON: it consumes nothing). Returns the next index, or -1. */
function expect(keys: readonly string[], i: number, key: string, value: unknown, sent: Record<string, unknown>): number {
  if (value === undefined) return i
  return keys[i] === key && same(value, sent[key]) ? i + 1 : -1
}

/** A record spread with its defaults: `{...defaults, ...over}`. The defaults' keys and values as parallel arrays, in order. */
class Defaults {
  readonly keys: string[]
  readonly values: unknown[]
  readonly record: Record<string, unknown>
  constructor(record: Record<string, unknown>) { this.record = record; this.keys = Object.keys(record); this.values = this.keys.map(k => record[k]) }
}
const NO_DEFAULTS = new Defaults({})
const RETRY = new Defaults(DEFAULT_RETRY_POLICY)
const COMPACTION = new Defaults(DEFAULT_COMPACTION_POLICY)

/** `{...defaults, ...over}` serialises to the sent record: the defaults' keys in order (a given value replaces the default in place, an undefined one drops the key), then the other own keys of `over`. Spread copies own enumerable properties of any object, so only a non-object or an array is refused. */
function merged(defaults: Defaults, over: unknown, keys: readonly string[], sent: Record<string, unknown>): boolean {
  if (over === null) over = undefined
  if (over !== undefined && (typeof over !== "object" || Array.isArray(over))) return false
  const o = over as Record<string, unknown> | undefined
  const dk = defaults.keys
  let i = 0
  for (let n = 0; n < dk.length; n++) {
    const k = dk[n] as string
    let v: unknown = defaults.values[n]
    // Spread copies own enumerable properties only: an inherited or non-enumerable `k` leaves the default.
    if (o !== undefined && ownEnumerable.call(o, k)) v = o[k]
    i = expect(keys, i, k, v, sent)
    if (i < 0) return false
  }
  if (o !== undefined) {
    for (const k in o) {
      if (!hasOwn.call(o, k) || hasOwn.call(defaults.record, k)) continue
      i = expect(keys, i, k, o[k], sent)
      if (i < 0) return false
    }
  }
  return i === keys.length
}

/** The settings the core was last sent: the JSON text (the exact comparison), its parsed copy and the key order of each record. */
export class SentSettings {
  text = ""
  private sent: Record<string, any> = {}
  private top: string[] = []
  private stream: string[] = []
  private retry: string[] = []
  private compaction: string[] = []
  private progress = 0

  set(text: string): void {
    this.text = text
    const sent = JSON.parse(text) as Record<string, any>
    this.sent = sent
    this.top = Object.keys(sent)
    const keys = (v: unknown) => (plain(v) ? Object.keys(v) : [])
    this.stream = keys(sent.stream)
    this.retry = keys(sent.retry)
    this.compaction = keys(sent.compaction)
    this.progress = keys(sent.progress).length
  }

  /** True only when `resolveSettings(raw)` (with the extensions as names) serialises to the text last sent. */
  unchanged(raw: any): boolean {
    try {
      const sent = this.sent, top = this.top
      let i = 0
      const extensions = raw?.extensions
      if (extensions !== undefined) {
        if (!Array.isArray(extensions) || top[i++] !== "extensions") return false
        const names = sent.extensions
        if (!Array.isArray(names) || names.length !== extensions.length) return false
        for (let n = 0; n < extensions.length; n++) {
          const e = extensions[n]
          if (typeof e !== "object" || e === null || typeof e.name !== "string" || e.name !== names[n]) return false
        }
      }
      if (top[i++] !== "stream" || !merged(NO_DEFAULTS, raw?.stream, this.stream, sent.stream)) return false
      if (top[i++] !== "retry" || !merged(RETRY, raw?.retry, this.retry, sent.retry)) return false
      if (top[i++] !== "compaction" || !merged(COMPACTION, raw?.compaction, this.compaction, sent.compaction)) return false
      if (top[i++] !== "progress") return false
      const progress = sent.progress
      const p = raw?.progress
      const partial = p?.partialIntervalMs ?? DEFAULT_PROGRESS_POLICY.partialIntervalMs
      const output = p?.outputIntervalMs ?? DEFAULT_PROGRESS_POLICY.outputIntervalMs
      if (!same(partial, progress.partialIntervalMs) || !same(output, progress.outputIntervalMs)) return false
      if (this.progress !== 2) return false
      if (top[i++] !== "contextRetentionMs" || !same(raw?.contextRetentionMs ?? DEFAULT_CONTEXT_RETENTION_MS, sent.contextRetentionMs)) return false
      if (top[i++] !== "toolExecution" || !same(raw?.toolExecution ?? "parallel", sent.toolExecution)) return false
      if (top[i++] !== "steeringMode" || !same(raw?.steeringMode ?? "one-at-a-time", sent.steeringMode)) return false
      if (top[i++] !== "followUpMode" || !same(raw?.followUpMode ?? "one-at-a-time", sent.followUpMode)) return false
      // The retention is live too and travels last, only when it is not the core's default.
      const retention = documentRetentionWire(raw)
      if (retention !== undefined && (top[i++] !== "documentRetentionMs" || !same(retention, sent.documentRetentionMs))) return false
      return i === top.length
    } catch {
      return false
    }
  }
}
