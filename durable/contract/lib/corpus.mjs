// The scenario manifest and the implementation list: loading, validation, and the drift check against the reference checkout.
import { createHash } from "node:crypto"
import { existsSync, readFileSync } from "node:fs"
import { join, resolve } from "node:path"
import { CONTRACT_DIR, piSource } from "./run.mjs"
import { parseToml } from "./toml.mjs"

export const REPO = resolve(CONTRACT_DIR, "..", "..")
export const loadCorpus = () => parseToml(readFileSync(join(CONTRACT_DIR, "corpus.toml"), "utf8"))
export const loadImpls = () => parseToml(readFileSync(join(CONTRACT_DIR, "impls.toml"), "utf8")).impl ?? []
export const LANES = ["dcore-session", "dcore-loop", "dcore-store", "dcore-host", "do-core-design"]

/** Problems with the manifest itself: unknown owners, duplicate ids, rows without a runner or a reason. */
export function validateCorpus(corpus) {
	const problems = []
	const ids = new Set()
	for (const s of corpus.scenario) {
		if (ids.has(s.id)) problems.push(`${s.id}: duplicate id`)
		ids.add(s.id)
		if (!LANES.includes(s.owner)) problems.push(`${s.id}: owner ${s.owner} is not one of ${LANES.join(", ")}`)
		if (!s.runner) problems.push(`${s.id}: no runner`)
		if (["skipped", "pending"].includes(s.status) && !s.reason) problems.push(`${s.id}: status ${s.status} needs a reason`)
		if (s.normalisers?.length) problems.push(`${s.id}: normalisers are not allowed in acceptance runs (CONTRACT 2.2)`)
	}
	for (const s of corpus.scenario) if (s.scenario && !ids.has(s.scenario)) problems.push(`${s.id}: unknown scenario ${s.scenario}`)
	return problems
}

/** Rows whose source file no longer hashes to the manifest's sha256 in the reference checkout. */
export function driftedSources(corpus) {
	const pi = piSource()
	const out = []
	for (const s of corpus.scenario) {
		if (!s.sha256) continue
		const file = join(pi, s.source)
		if (!existsSync(file)) { out.push({ id: s.id, source: s.source, problem: "missing" }); continue }
		const got = createHash("sha256").update(readFileSync(file)).digest("hex")
		if (got !== s.sha256) out.push({ id: s.id, source: s.source, problem: "changed", expected: s.sha256, got })
	}
	return out
}
