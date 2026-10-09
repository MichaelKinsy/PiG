// nightly-record.mjs <night dir> <nightly.jsonl> <core>: one night from rule-a.out (track-row.sh's JSON line) and
// native.jsonl (the native bench's timing lines), judged against the earlier nights in nightly.jsonl, appended to it,
// and printed as one line (lib/nightly.mjs).
import { appendFileSync, existsSync, readdirSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { describeNight, judge, ruleB, timeTravel } from "../lib/nightly.mjs"

const [dir, file, core] = process.argv.slice(2)
const lines = path => readFileSync(path, "utf8").split("\n").filter(Boolean).map(l => JSON.parse(l))
const a = lines(join(dir, "rule-a.out")).at(-1)
const b = ruleB(lines(join(dir, "native.jsonl")))
const pick = (c, k) => c.compare[k]
const night = {
	at: new Date().toISOString(),
	core,
	size: a.turns,
	warmTurns: { a: a.warmTurns, b: b.nativeWarmTurns },
	pig: { warm: pick(a, "warm").pig, warmP95: pick(a, "warmP95").pig, cold: pick(a, "cold").pig, nativeWarm: b.nativeWarm, nativeWarmP95: b.nativeWarmP95, nativeCold: b.nativeCold },
	pi: { warm: pick(a, "warm").piRun, warmP95: pick(a, "warmP95").piRun, cold: pick(a, "cold").piRun },
	run: dir,
}
// Time travel ran only when the core has scenarios/time-travel.mjs and the contract environment was set.
const ttFiles = readdirSync(dir).filter(f => /^timetravel-\d+\.json$/.test(f)).sort()
// nightly.sh writes timetravel-<n>.txt for every run it started; a run without its .json gave no result.
const ttMissing = readdirSync(dir).filter(f => /^timetravel-\d+\.txt$/.test(f) && !ttFiles.includes(f.replace(/\.txt$/, ".json"))).sort()
const tt = ttFiles.length ? timeTravel(ttFiles.map(f => JSON.parse(readFileSync(join(dir, f), "utf8")))) : undefined
if (tt) Object.assign(night, { pig: { ...night.pig, ...tt.pig }, pi: { ...night.pi, ...tt.pi }, timeTravel: tt.verdicts })
if (ttMissing.length) night.timeTravelMissing = ttMissing
const history = existsSync(file) ? lines(file) : []
const verdicts = judge(history, night)
appendFileSync(file, JSON.stringify({ ...night, drift: verdicts.filter(v => v.drift).map(v => v.key) }) + "\n")
console.log(describeNight(core, verdicts, tt?.verdicts, ttMissing))
