#!/usr/bin/env node
// Per-backend tick tables of the Storage adapters, measured on the reference's own code (scenarios/storage-ticks.mjs) and written to
// durable/core/session/hopcost/testdata/ticks.json. hopcost's tests derive each call's ticks from the model and require them to
// equal this table, so a change of the reference's adapters or of the model shows as a drift here, not as a reordered commit later.
//   node tools/gen-hopticks.mjs            write the table
//   node tools/gen-hopticks.mjs --check    regenerate and fail when the committed table differs
// Profiles: gate (the contract gate's lazy MemoryStorage proxy), gate-capsql (SqliteStorage over the capsql facade, opened directly)
// and node (the Node SQLite adapter, CONTRACT_STORAGE_PROFILE=node). A production adapter without a measurement here has no table.
import { readFileSync, writeFileSync } from "node:fs"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import { runScenario } from "../lib/run.mjs"

const here = dirname(fileURLToPath(import.meta.url))
const contract = join(here, "..")
const target = join(contract, "..", "core", "session", "hopcost", "testdata", "ticks.json")
const pin = readFileSync(join(contract, "PIN"), "utf8").split("\n").map(l => l.split(/\s+/)).find(f => f[0] === "main")?.[1]

const PROFILES = { gate: { storage: "memory", env: {} }, "gate-capsql": { storage: "sqlite", env: {} }, node: { storage: "sqlite", env: { CONTRACT_STORAGE_PROFILE: "node" } } }
const work = mkdtempSync(join(tmpdir(), "hopticks-"))
const profiles = {}
for (const [name, p] of Object.entries(PROFILES)) {
	const r = await runScenario({ script: join(contract, "scenarios", "storage-ticks.mjs"), out: join(work, name), name: "storage-ticks", args: ["--storage", p.storage], env: p.env })
	if (r.code !== 0) { console.error(`storage-ticks (${name}) failed: ${r.stderr}`); process.exit(1) }
	profiles[name] = Object.fromEntries(r.stdout.trim().split("\n").map(line => { const i = line.lastIndexOf(" "); return [line.slice(0, i), Number(line.slice(i + 1))] }))
}
const text = JSON.stringify({ source: "durable/contract/scenarios/storage-ticks.mjs", pin, profiles }, null, "\t") + "\n"
if (process.argv.includes("--check")) {
	if (readFileSync(target, "utf8") !== text) { console.error(`${target} is stale: run node tools/gen-hopticks.mjs`); process.exit(1) }
	console.log("ticks.json is current")
} else {
	writeFileSync(target, text)
	console.log(`wrote ${target}`)
}
