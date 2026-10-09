// The ABI 1 constants. abi.json is generated from the Go tables (abi/spec/cmd/abigen); nothing here is hand-copied.
import tables from "../../../abi/abi.json" with { type: "json" }

type Row = { name: string; number: number }
const byName = (rows: Row[]) => Object.fromEntries(rows.map(r => [r.name, r.number])) as Record<string, number>
const nameOf = (rows: Row[]) => (n: number) => rows.find(r => r.number === n)?.name ?? `#${n}`

export const ABI_ID: string = tables.id
export const EVENT = byName(tables.events) as Record<"open" | "rows" | "submit" | "model_event" | "model_bytes" | "tool_progress" | "tool_done" | "hook_done" | "phase_done" | "tx" | "timer" | "abort" | "registry" | "api" | "close" | "inspect", number>
export const EFFECT = byName(tables.effects) as Record<"timer" | "timer_clear" | "model_context" | "model_http" | "cancel" | "tool" | "hook" | "phase" | "env" | "liveness" | "section" | "deferred" | "callback", number>
export const NOTICE = byName(tables.notices) as Record<"published" | "api_result" | "tx_result" | "report" | "sidecar" | "progress_ack", number>
export const STATUS = byName(tables.status) as Record<"ok" | "rejected" | "fatal", number>
export const eventName = nameOf(tables.events)
export const effectName = nameOf(tables.effects)
export const noticeName = nameOf(tables.notices)
export const WASM_EXPORTS: readonly string[] = tables.exports.map(e => e.name)
