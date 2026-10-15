#!/usr/bin/env node
// fixtures [sizes...] [--force]: write the W1 fixtures with pi-durable (the reference checkout): a store of N turns of
// history from the scripted model, one fixture per size under <cache>/fixtures. The seed's final bench fingerprint must
// equal the published one (lib/workload.mjs SEED_FINGERPRINTS) or the fixture is rejected. 10,000 and 25,000 turns are the
// next smaller fixture extended by tools/extend-fixture.mjs (EXTENDED_FINGERPRINTS).
import { execFileSync } from "node:child_process"
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import { CONTRACT_DIR, legacySource, piSource, runScenario } from "../lib/run.mjs"
import { EXTENDED_FINGERPRINTS, EXTENDED_FROM, SEED_FINGERPRINTS } from "../lib/workload.mjs"

export const cache = () => process.env.DURABLE_CONTRACT_CACHE ?? join(CONTRACT_DIR, ".cache")
export const fixturePath = (kind, size) => join(cache(), "fixtures", `${kind}-${size}.sqlite`)

/** W4a: compaction on, a 200k-token window and 8 KiB tool results, so head markers appear every ~150 turns (CONTRACT section 6). */
/** v1.0.4: the same seed written by the release PIN names, so the reference main and every candidate must read and extend a 1.0.4 store. */
const KINDS = { "pi-main": [], "v1.0.4": [], compact: ["--compaction", "--window", "200000", "--payload-kb", "8"] }

export async function seed(size, { force = false, kind = "pi-main" } = {}) {
	const out = fixturePath(kind, size)
	if (existsSync(out) && !force) return out
	mkdirSync(join(cache(), "fixtures"), { recursive: true })
	if (kind === "pi-main" && EXTENDED_FROM[size]) return extend(size, out)
	const work = join(cache(), "work", `seed-${kind}-${size}`)
	rmSync(work, { recursive: true, force: true })
	const r = await runScenario({ script: join(CONTRACT_DIR, "scenarios", "bench.mjs"), out: work, name: `seed-${size}`, source: kind === "v1.0.4" ? legacySource() : undefined, args: ["--db", join(work, "unused.db"), "--seed", String(size), "--turns", "0", ...KINDS[kind]], timeoutMs: 3 * 3600_000 })
	if (r.code !== 0) throw new Error(`seeding ${size} failed: ${r.stderr}`)
	const { bench } = JSON.parse(r.stdout.trim().split("\n").at(-1))
	if (kind !== "compact" && SEED_FINGERPRINTS[size] && SEED_FINGERPRINTS[size] !== bench) throw new Error(`seed ${size}: fingerprint ${bench}, published ${SEED_FINGERPRINTS[size]}`)
	const db = new DatabaseSync(r.stores[0])
	db.exec("PRAGMA wal_checkpoint(TRUNCATE)")
	db.close()
	copyFileSync(r.stores[0], out)
	writeMeta(out, { size, kind, bench })
	return out
}
/** A long history (10,000 or 25,000 turns): the next smaller fixture extended with tools/extend-fixture.mjs, fingerprint-checked. */
async function extend(size, out) {
	const from = EXTENDED_FROM[size]
	const base = await seed(from)
	const work = join(cache(), "work", `extend-${size}.sqlite`)
	mkdirSync(join(cache(), "work"), { recursive: true })
	rmSync(work, { force: true })
	copyFileSync(base, work)
	const line = execFileSync(process.execPath, ["--conditions=source", "--experimental-strip-types", "--no-warnings", join(CONTRACT_DIR, "tools", "extend-fixture.mjs"), work, String(from), String(size)], { cwd: join(piSource(), "packages", "durable"), env: { ...process.env, CONTRACT_PI_SOURCE: piSource() }, encoding: "utf8", stdio: ["ignore", "pipe", "inherit"], maxBuffer: 1 << 20 }).trim().split("\n").at(-1)
	const { bench } = JSON.parse(line)
	if (bench !== EXTENDED_FINGERPRINTS[size]) throw new Error(`fixture ${size}: fingerprint ${bench}, want ${EXTENDED_FINGERPRINTS[size]} (extended from ${from})`)
	copyFileSync(work, out)
	rmSync(work, { force: true })
	await writeMeta(out, { size, kind: "pi-main", bench, extendedFrom: from })
	return out
}
const writeMeta = (path, meta) => import("node:fs").then(fs => fs.writeFileSync(`${path}.json`, JSON.stringify(meta) + "\n"))

export async function cli(args) {
	const force = args.includes("--force")
	const kind = args.includes("--compact") ? "compact" : "pi-main"
	const sizes = args.filter(a => /^\d+$/.test(a)).map(Number)
	for (const size of sizes.length ? sizes : kind === "compact" ? [1000, 3500] : [50, 250, 1000, 3500]) {
		const t0 = Date.now()
		console.log(`fixture ${kind}-${size}: ${await seed(size, { force, kind })} (${Date.now() - t0} ms)`)
	}
}
if (import.meta.url === `file://${process.argv[1]}`) await cli(process.argv.slice(2))
