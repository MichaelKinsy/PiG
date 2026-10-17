// The known-failure baseline (baseline.toml): the differential rows (F) that differ from Pi today, each with its owner and a shrunk
// repro. A listed row stays red in every report, and the gate's exit status does not count it; any other failing row does, and so does
// a listed row that passes on every build (the entry is deleted in the change that fixes it, so the file only shrinks). Only
// differential rows may be listed: no other row, level or test is relaxed (CONTRACT 5.9; the owner's decision of 2026-10-08).
import { existsSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { CONTRACT_DIR } from "./run.mjs"
import { parseToml } from "./toml.mjs"

export const baselineFile = () => process.env.CONTRACT_BASELINE ?? join(CONTRACT_DIR, "baseline.toml")
export const loadBaseline = () => (existsSync(baselineFile()) ? parseToml(readFileSync(baselineFile(), "utf8")).known ?? [] : [])

/** Problems with the baseline itself: an entry that is not a differential row of the manifest, a duplicate, or one without owner, cause or repro. */
export function validateBaseline(known, corpus) {
	const problems = []
	const rows = new Map(corpus.scenario.map(r => [r.id, r]))
	const seen = new Set()
	for (const k of known) {
		if (seen.has(k.id)) problems.push(`${k.id}: listed twice`)
		seen.add(k.id)
		if (rows.get(k.id)?.runner !== "diff") problems.push(`${k.id}: only differential rows (runner diff) may be in the baseline`)
		for (const key of ["owner", "cause", "repro"]) if (!k[key]) problems.push(`${k.id}: no ${key}`)
	}
	return problems
}

/**
 * Split the results of one build against the baseline: `known` are failing rows the baseline lists, `fresh` are failing rows it does not,
 * `passing` are listed rows that passed here. The control (`pi`) has no baseline: every failure is fresh.
 */
export function judge(impl, results, known = loadBaseline()) {
	const listed = impl === "pi" || impl === "ts" ? new Set() : new Set(known.map(k => k.id))
	return {
		known: results.filter(r => r.status === "fail" && listed.has(r.id)),
		fresh: results.filter(r => r.status === "fail" && !listed.has(r.id)),
		passing: results.filter(r => r.status === "pass" && listed.has(r.id)),
	}
}

/** Listed rows that passed on every build that ran them: the entries to delete. */
export function stale(perImpl) {
	const builds = Object.entries(perImpl).filter(([impl]) => impl !== "pi" && impl !== "ts")
	if (!builds.length) return []
	const [[, first], ...rest] = builds
	return first.passing.map(r => r.id).filter(id => rest.every(([, j]) => j.passing.some(r => r.id === id)))
}

/** Lines for a report. */
export function describe(impl, j) {
	const out = []
	if (j.known.length) out.push(`known failures (baseline.toml) on ${impl}: ${j.known.map(r => r.id).join(" ")}`)
	if (j.fresh.length) out.push(`NEW FAILURES on ${impl} (not in baseline.toml): ${j.fresh.map(r => r.id).join(" ")}`)
	return out
}
