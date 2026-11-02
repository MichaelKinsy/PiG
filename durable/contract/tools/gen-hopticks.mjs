#!/usr/bin/env node
// Per-backend tick tables measured on the reference's own code (PIN `main`): the Storage adapters (scenarios/storage-ticks.mjs, written to
// durable/core/session/hopcost/testdata/ticks.json) and the Session over them (scenarios/session-ticks.mjs, written to
// testdata/session_ticks.json: what a Transaction method and a whole commit take) and a task's runtime.commit (scenarios/gated-ticks.mjs, gated_ticks.json). hopcost's tests derive each call's ticks from the model and
// require them to equal these tables, so a change of the reference's adapters or of the model shows as a drift here, not as a reordered commit later.
//   node tools/gen-hopticks.mjs            write the table
//   node tools/gen-hopticks.mjs --check    regenerate and fail when the committed table differs
// Profiles: gate (the contract gate's lazy MemoryStorage proxy), gate-capsql (SqliteStorage over the capsql facade, opened directly)
// and node (the Node SQLite adapter, CONTRACT_STORAGE_PROFILE=node). A production adapter without a measurement here has no table.
import { readFileSync, writeFileSync } from "node:fs"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import { piSource, runScenario } from "../lib/run.mjs"

const here = dirname(fileURLToPath(import.meta.url))
const contract = join(here, "..")
const testdata = join(contract, "..", "core", "session", "hopcost", "testdata")
const pin = readFileSync(join(contract, "PIN"), "utf8").split("\n").map(l => l.split(/\s+/)).find(f => f[0] === "main")?.[1]

const PROFILES = { gate: { storage: "memory", env: {} }, "gate-capsql": { storage: "sqlite", env: {} }, node: { storage: "sqlite", env: { CONTRACT_STORAGE_PROFILE: "node" } } }
const work = mkdtempSync(join(tmpdir(), "hopticks-"))
const tables = [["storage-ticks", "ticks.json"], ["session-ticks", "session_ticks.json"], ["gated-ticks", "gated_ticks.json"]]
const texts = new Map()
for (const [scenario, file] of tables) {
	const profiles = {}
	for (const [name, p] of Object.entries(PROFILES)) {
		const r = await runScenario({ script: join(contract, "scenarios", `${scenario}.mjs`), out: join(work, `${scenario}-${name}`), name: scenario, args: ["--storage", p.storage], env: p.env, source: piSource() })
		if (r.code !== 0) { console.error(`${scenario} (${name}) failed: ${r.stderr}`); process.exit(1) }
		profiles[name] = Object.fromEntries(r.stdout.trim().split("\n").map(line => { const i = line.lastIndexOf(" "); return [line.slice(0, i), Number(line.slice(i + 1))] }))
	}
	texts.set(file, JSON.stringify({ source: `durable/contract/scenarios/${scenario}.mjs`, pin, profiles }, null, "\t") + "\n")
}
let stale = false
for (const [file, text] of texts) {
	const target = join(testdata, file)
	if (process.argv.includes("--check")) {
		if (readFileSync(target, "utf8") !== text) { console.error(`${target} is stale: run node tools/gen-hopticks.mjs`); stale = true }
	} else {
		writeFileSync(target, text)
		console.log(`wrote ${target}`)
	}
}
if (process.argv.includes("--check")) {
	if (stale) process.exit(1)
	console.log("ticks.json and session_ticks.json are current")
}
