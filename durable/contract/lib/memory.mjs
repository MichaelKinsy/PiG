// The memory bound of a bench row (CONTRACT section 5, "resource bound"): a candidate whose memory keeps growing while it
// runs identical turns fails its row with the numbers, instead of trapping later (the TinyGo core doubled its linear memory
// to 3 GiB on a 3500-turn history) or being killed by the host. A run writes one sample per measured turn to
// CONTRACT_MEM ({"turn":N,"rss":BYTES,"linear"?:BYTES}); `linear` is the summed linear memory of the Wasm cores the facade
// made (the Wasm builds), `rss` the process's resident set (all builds; Go builds return freed memory to the OS first).
// The first measured turn is the warm-up (caches, the cold open, the heap's first growth); the growth is measured from
// the end of turn 0 to the end of the last turn. Linear memory only grows, in steps (TinyGo and Go double the heap), so a run
// may grow it by the per-turn bound times its turns, by 32 MiB or by twice what turn 0 left (two doublings), whichever is
// larger; churn that never stops doubles again and again (TinyGo: 96 MiB to 3 GiB in ten turns) and fails. The resident set
// of a Go build moves with the collector and the page cache: it may grow by the bound times its turns plus 16 MiB.
import { existsSync, readFileSync } from "node:fs"

const MiB = 1024 * 1024
/** MiB of growth per measured turn a candidate may show; override with CONTRACT_MEM_BOUND_MIB (linear and rss alike). */
export const defaultBoundMiB = 4

export function readSamples(path) {
	if (!path || !existsSync(path)) return []
	return readFileSync(path, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))
}

/** @returns {{turns:number, metric:"linear"|"rss", first:number, last:number, perTurn:number, rss:number}|undefined} undefined when there are fewer than three samples. */
export function memorySummary(samples) {
	if (samples.length < 3) return undefined
	const metric = samples.every(s => typeof s.linear === "number") ? "linear" : "rss"
	const first = samples[0][metric], last = samples.at(-1)[metric]
	return { turns: samples.length, metric, first, last, perTurn: (last - first) / (samples.length - 1), rss: samples.at(-1).rss }
}

/** "+0.0 MiB/turn linear 192 MiB over 10 turns" */
export function memoryDescribe(s) {
	return `${s.perTurn >= 0 ? "+" : ""}${(s.perTurn / MiB).toFixed(1)} MiB/turn ${s.metric} ${Math.round(s.last / MiB)} MiB over ${s.turns} turns`
}

/** The bound judges a candidate: the pi control is the reference (its resident set grows 6-10 MiB per turn on the 1000- and 3500-turn rows) and is reported, never failed. */
export const boundApplies = impl => impl !== "pi"

/** The failure a summary shows against the bound, or undefined. */
export function overBound(s, boundMiB = Number(process.env.CONTRACT_MEM_BOUND_MIB ?? defaultBoundMiB)) {
	if (!s) return undefined
	const steady = boundMiB * MiB * (s.turns - 1)
	const allowed = s.metric === "linear" ? Math.max(steady, 32 * MiB, 2 * s.first) : steady + 16 * MiB
	if (s.last - s.first <= allowed) return undefined
	return { problem: `memory grows ${(s.perTurn / MiB).toFixed(1)} MiB per measured turn (${s.metric}: ${Math.round(s.first / MiB)} MiB after turn 0, ${Math.round(s.last / MiB)} MiB after turn ${s.turns - 1}); it may grow ${Math.round(allowed / MiB)} MiB over these turns (${boundMiB} MiB per turn${s.metric === "linear" ? ", or two heap doublings" : " plus 16 MiB"})` }
}
