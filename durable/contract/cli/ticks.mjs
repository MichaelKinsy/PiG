#!/usr/bin/env node
// ticks <out-dir> <row> <impl>: the microtask-hop profile of a row's commits, reference against candidate.
// Run the row first with CONTRACT_TICKS=1 (`CONTRACT_TICKS=1 contract run E20 --impl native --out DIR`): each commit then
// carries `tick`, the passes of the microtask queue since the process started (hooks/register.mjs). The table shows the hops
// between consecutive commits. Pi orders concurrent work by Promise hops, so a candidate whose hops between commits are far
// smaller than the reference's runs a background chain ahead of the client call that follows it in Pi (E20, E25).
import { existsSync, readdirSync } from "node:fs"
import { join } from "node:path"
import { readTrace } from "../lib/rowdiff.mjs"

const [dir, row, impl] = process.argv.slice(2)
if (!dir || !row || !impl) { console.error("usage: ticks <out-dir> <row> <impl>"); process.exit(2) }
const commits = side => {
	const d = join(dir, side)
	const file = existsSync(d) && readdirSync(d).find(f => f.startsWith(`${row}.`) && f.endsWith(".trace"))
	if (!file) { console.error(`no ${row} trace in ${d}`); process.exit(2) }
	return readTrace(join(d, file)).filter(l => l.t === "commit" && l.seq !== undefined)
}
const ref = commits("ref"), cand = commits(impl)
const delta = (list, i) => (list[i]?.tick === undefined ? undefined : list[i].tick - (list[i - 1]?.tick ?? 0))
if (ref.every(c => c.tick === undefined)) { console.error("the commits carry no tick: run the row with CONTRACT_TICKS=1"); process.exit(2) }
console.log("seq    ref hops   " + `${impl} hops`)
for (let i = 0; i < Math.max(ref.length, cand.length); i++) console.log(`${String(ref[i]?.seq ?? cand[i]?.seq).padStart(3)} ${String(delta(ref, i) ?? "-").padStart(10)} ${String(delta(cand, i) ?? "-").padStart(10)}`)
