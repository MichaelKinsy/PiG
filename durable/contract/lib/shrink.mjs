// Shrink a failing seed of the differential ordering scenario to a small program (`contract soak --shrink`). Every reduction keeps
// every random draw of the seed (scenarios/diff-ordering.mjs), so a reduced program is the same run with parts removed, and the
// result is a command a lane owner can rerun. Greedy: try each cut, keep it when the candidate still differs from Pi, repeat.
import { join } from "node:path"
import { diffRows, gate } from "./gate.mjs"
import { readTrace } from "./rowdiff.mjs"

const HOOKS = ["beforeTool", "afterTool", "beforeRequest", "afterResponse", "afterTools"]

/** The reduction state of a plan: what is kept. */
const initial = plan => ({
	hooks: HOOKS.filter(h => plan.hooks[h] !== undefined),
	observers: plan.observers.map((_, i) => i),
	arrivals: plan.arrivals.map((_, i) => i),
	single: false, rounds: undefined, calls: undefined, streaming: plan.streaming,
	conversations: new Set([...plan.inputs, ...plan.arrivals].map(p => p.conv)).size,
})
const list = items => (items.length ? items.join(",") : "none")
export const argsOf = state => [
	"--hooks", list(state.hooks), "--observers", list(state.observers), "--arrivals", list(state.arrivals),
	...(state.single ? ["--conversations", "1"] : []), ...(state.rounds === undefined ? [] : ["--rounds", String(state.rounds)]),
	...(state.calls === undefined ? [] : ["--calls", String(state.calls)]), ...(state.streaming ? [] : ["--streaming", "off"]),
]
/** The cuts that apply to a state, most drastic first. */
function cuts(state) {
	const out = []
	if (!state.single && state.conversations > 1) out.push({ ...state, single: true })
	if (state.arrivals.length) out.push({ ...state, arrivals: [] })
	if (state.observers.length) out.push({ ...state, observers: [] })
	if (state.hooks.length) out.push({ ...state, hooks: [] })
	if (state.streaming) out.push({ ...state, streaming: false })
	for (const rounds of [0, 1, 2]) if (state.rounds === undefined || state.rounds > rounds) out.push({ ...state, rounds })
	for (const calls of [1, 2]) if (state.calls === undefined || state.calls > calls) out.push({ ...state, calls })
	for (const i of state.arrivals) out.push({ ...state, arrivals: state.arrivals.filter(x => x !== i) })
	for (const i of state.observers) out.push({ ...state, observers: state.observers.filter(x => x !== i) })
	for (const h of state.hooks) out.push({ ...state, hooks: state.hooks.filter(x => x !== h) })
	return out
}

/** The plan line the scenario prints first. */
export const planOf = trace => JSON.parse(readTrace(trace).find(l => l.t === "out" && l.text.startsWith("plan ")).text.slice(5))

/**
 * Shrink seed `seed` on `profile` against `impl`. `row` is a failed result of the full seed (its reference trace gives the plan).
 * Returns the reduced arguments and the last failing detail; `log` reports each trial.
 */
export async function shrink({ impl, seed, profile, out, trace, log = () => {}, concurrency = 4 }) {
	let state = initial(planOf(trace))
	let detail
	const trial = async (candidate, n) => {
		const row = { ...diffRows(seed, seed, [profile])[0], id: `F-${profile}-${seed}-s${n}`, args: argsOf(candidate) }
		const [result] = await gate({ impl, out: join(out, `s${n}`), concurrency, rows: [row] })
		return result
	}
	let n = 0
	for (let progress = true; progress;) {
		progress = false
		for (const cut of cuts(state)) {
			const result = await trial(cut, ++n)
			log(`${result.status} ${argsOf(cut).join(" ")}`)
			if (result.status === "fail") { state = cut; detail = result.detail; progress = true; break }
		}
	}
	return { args: argsOf(state), detail }
}
