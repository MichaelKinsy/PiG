// The nightly wall-clock run's drift judgement (runner/nightly.sh). A night measures the dcore-integrate tip twice:
// Rule A (track-row.sh: PiG TinyGo at 3,500 turns in Miniflare with pi-durable main and the empty floor in the same rounds)
// and Rule B (the native build's own bench with its scripted model, no request path, no floor). Each night is compared
// with the median of the last WINDOW nights. A shared host slows every target together, so a metric drifts only when PiG
// is slower than its own recent median by more than LIMIT *and* its ratio to pi-durable's same-run value has grown by more
// than LIMIT too: a load spike moves pi as well and leaves the ratio alone. Rule B has no pi value of its own; its ratio is
// to pi's same-run warm p50, the nearest load control the night has.
export const WINDOW = 5
export const LIMIT = 0.1
export const METRICS = [
	{ key: "warm", rule: "A", pi: "warm", name: "Rule A warm p50" },
	{ key: "warmP95", rule: "A", pi: "warmP95", name: "Rule A warm p95" },
	{ key: "cold", rule: "A", pi: "cold", name: "Rule A cold p50" },
	{ key: "nativeWarm", rule: "B", pi: "warm", name: "Rule B warm p50" },
	{ key: "nativeWarmP95", rule: "B", pi: "warm", name: "Rule B warm p95" },
	{ key: "nativeCold", rule: "B", pi: "warm", name: "Rule B cold p50" },
]

// Time travel (robust-durable's scenarios/time-travel.mjs, when the core has it): a few runs per build at SIZE turns, each
// operation's wall time summed within a run (the scrub's S as-of reads together), then the median over the runs. A build's
// operation is judged against its own nights with pi's same operation in the same night as the control: keys
// `tt:<build>:<op>` in pig, `tt:<op>` in pi. Operations of a few milliseconds move by more than 10% on timer noise alone,
// so a time-travel drift must also be at least TT_MIN_MS slower than the median.
export const TT_MIN_MS = 5
const metricsOf = night => [
	...METRICS,
	...Object.keys(night.pig).filter(k => k.startsWith("tt:")).map(key => {
		const [, build, op] = key.split(":")
		return { key, rule: "T", pi: `tt:${op}`, name: `time travel ${build} ${op}`, minMs: TT_MIN_MS }
	}),
]

/**
 * A night's time-travel record from one or more `robust timetravel --json` results: per-operation medians for pi and each
 * build, and each build's verdict ("ok", or why its first failing run failed: a trap or a difference from pi). A build that
 * failed in any run has no timings.
 */
export function timeTravel(results) {
	const sums = {}, failures = {}
	for (const r of [results].flat()) {
		for (const [label, run] of Object.entries(r.runs)) {
			if (label !== "pi" && !run.ok) failures[label] ??= String(run.problem ?? "failed").slice(0, 160)
			const by = {}
			for (const o of run.ops) by[o.op] = (by[o.op] ?? 0) + o.ms
			for (const [op, ms] of Object.entries(by)) (sums[label] ??= {})[op] = [...(sums[label]?.[op] ?? []), ms]
		}
	}
	const pig = {}, pi = {}, verdicts = {}
	for (const [label, ops] of Object.entries(sums)) {
		if (label !== "pi") verdicts[label] = failures[label] ?? "ok"
		if (failures[label]) continue
		for (const [op, xs] of Object.entries(ops)) (label === "pi" ? pi : pig)[label === "pi" ? `tt:${op}` : `tt:${label}:${op}`] = median(xs)
	}
	return { pig, pi, verdicts }
}

const median = xs => { const s = [...xs].sort((a, b) => a - b); const m = s.length >> 1; return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2 }

/** Linear-interpolated quantile, as track-row.sh computes its own. */
export function quantile(xs, p) {
	const s = [...xs].sort((a, b) => a - b)
	const k = (s.length - 1) * p, f = Math.floor(k), c = Math.min(f + 1, s.length - 1)
	return s[f] + (s[c] - s[f]) * (k - f)
}

/** Rule B values from the native bench's timing lines ({open, turn[]} per fresh process): warm pools turn[1..], cold is open + turn[0]. */
export function ruleB(lines) {
	const warm = lines.flatMap(l => l.turn.slice(1))
	const cold = lines.map(l => l.open + l.turn[0])
	return { nativeWarm: quantile(warm, 0.5), nativeWarmP95: quantile(warm, 0.95), nativeCold: quantile(cold, 0.5), nativeWarmTurns: warm.length }
}

/**
 * One night's record: `pig` maps metric keys to PiG's values, `pi` pi-durable's same-run values (warm, warmP95, cold).
 * Returns per metric the night's value, the recent median, the ratio change and whether it drifted. Fewer than 3 earlier
 * nights judge nothing.
 */
export function judge(history, night, { window = WINDOW, limit = LIMIT } = {}) {
	const recent = history.slice(-window)
	return metricsOf(night).filter(m => typeof night.pig[m.key] === "number" && typeof night.pi[m.pi] === "number").map(m => {
		const prior = recent.filter(h => typeof h.pig[m.key] === "number" && typeof h.pi[m.pi] === "number")
		const value = night.pig[m.key], ratio = value / night.pi[m.pi]
		if (prior.length < 3) return { ...m, value, nights: prior.length, drift: false }
		const med = median(prior.map(h => h.pig[m.key])), medRatio = median(prior.map(h => h.pig[m.key] / h.pi[m.pi]))
		const slower = value / med - 1, ratioUp = ratio / medRatio - 1
		return { ...m, value, median: med, slower, ratioUp, nights: prior.length, drift: slower > limit && ratioUp > limit && value - med >= (m.minMs ?? 0) }
	})
}

const pct = x => `${x >= 0 ? "+" : ""}${Math.round(x * 100)}%`
/** One line per night: every metric with its change against the recent median, and the drifted ones named first. */
export function describeNight(core, verdicts, timeTravel, ttMissing = []) {
	const drifted = verdicts.filter(v => v.drift)
	const failed = Object.entries(timeTravel ?? {}).filter(([, v]) => v !== "ok")
	const tt = timeTravel ? ` Time travel: ${failed.length ? failed.map(([b, v]) => `${b} FAIL (${v})`).join(", ") : `equal to pi on ${Object.keys(timeTravel).join(", ")}`}.` : ""
	// A run that wrote no result (the reference failed, or robust could not start) is named, never dropped silently.
	const lost = ttMissing.length ? ` Time travel: ${ttMissing.length} run(s) gave no result (${ttMissing.join(", ")}).` : ""
	const parts = verdicts.map(v => `${v.name} ${Math.round(v.value)} ms${v.median === undefined ? ` (${v.nights} earlier nights, not judged)` : ` (median ${Math.round(v.median)}, ${pct(v.slower)}; vs pi ${pct(v.ratioUp)})`}`)
	return `${drifted.length ? `DRIFT ${drifted.map(v => v.name).join(", ")}. ` : "no drift. "}${core}: ${parts.join("; ")}.${tt}${lost}`
}
