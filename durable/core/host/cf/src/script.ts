// The event script format shared with abi/event.go (ParseScript): one event per line,
// `name [now=F] [id=N] [phase=N] [skipped=N] [wait=N] [payload]`, where payload is JSON, `hex:<digits>`, or for rows a JSON array of
// {"id","cols","rows"} with values null, a string (text), {"i":N}, {"f":X} or {"b":"hex"}.
import { EVENT } from "./abi.ts"
import { payloadBytes, type Rows } from "./wire.ts"

export type ScriptEvent = { name: string; kind: number; now: number; payload?: Uint8Array; rows?: Rows[] }

const hex = (s: string) => Uint8Array.from(s.match(/../g) ?? [], b => parseInt(b, 16))
const NAMES = EVENT as Record<string, number>

function value(cell: unknown): unknown {
  if (cell === null || typeof cell === "string") return cell
  const c = cell as { i?: number; f?: number; b?: string }
  if (c.i !== undefined) return c.i
  if (c.f !== undefined) return c.f
  if (c.b !== undefined) return hex(c.b)
  throw new Error(`unknown value ${JSON.stringify(cell)}`)
}

export function parseScript(text: string): ScriptEvent[] {
  const out: ScriptEvent[] = []
  for (const raw of text.split("\n")) {
    const line = raw.trim()
    if (!line || line.startsWith("#")) continue
    const space = line.indexOf(" ")
    const name = space < 0 ? line : line.slice(0, space)
    const kind = NAMES[name]
    if (kind === undefined) throw new Error(`unknown event ${name}`)
    let rest = space < 0 ? "" : line.slice(space + 1)
    const o = { now: 0, id: 0, phase: 0, skipped: 0, wait: 0 }
    for (;;) {
      rest = rest.trimStart()
      const m = /^(now|id|phase|skipped|wait)=(\S+)(?:\s+|$)/.exec(rest)
      if (!m) break
      o[m[1] as keyof typeof o] = Number(m[2])
      rest = rest.slice(m[0].length)
    }
    const body = rest.trim()
    const ev: ScriptEvent = { name, kind, now: o.now }
    const bytes = body.startsWith("hex:") ? hex(body.slice(4)) : body ? new TextEncoder().encode(body) : undefined
    switch (kind) {
      case EVENT.rows:
        ev.rows = (JSON.parse(body) as { id: number; cols: number; rows: unknown[][] }[]).map(a => ({ id: a.id, cols: a.cols, rows: a.rows.map(r => r.map(value)) }))
        break
      case EVENT.model_event: case EVENT.tx: case EVENT.api: ev.payload = payloadBytes([["u32", o.id]], bytes); break
      case EVENT.model_bytes: case EVENT.tool_done: case EVENT.hook_done: case EVENT.phase_done: ev.payload = payloadBytes([["u32", o.id], ["u8", o.phase]], bytes); break
      case EVENT.tool_progress: ev.payload = payloadBytes([["u32", o.id], ["u8", o.phase], ["u32", o.skipped], ["u32", o.wait]], bytes); break
      case EVENT.timer: ev.payload = payloadBytes([["u32", o.id]]); break
      default: ev.payload = bytes
    }
    out.push(ev)
  }
  return out
}
