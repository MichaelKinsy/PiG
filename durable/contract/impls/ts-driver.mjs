// The TypeScript control (durable-core-ts de1fac200, bake-off lane L6) as a contract implementation: the same bench scenario
// program as scenarios/bench.mjs, but the turns run in the vendored synchronous core (impls/ts-core) over capsql instead of
// in pi-durable. It is the second oracle (the task's "TS control") and the proof that the gate discriminates: it is a real
// reimplementation of pi-durable's writes, so it passes where it is faithful and fails where it is not.
//   ts-driver.mjs --db <store> [--turns N] [--tools 8] [--recover] [--stub js|jsincr|core] [--sidecar] [--payload-kb K]
// The scripted-model call is answered in the host from the parsed context (durable-bench's next()); `ctx` fingerprints need
// the full pi-ai Context, which this core does not build, so its model lines carry only the bench fingerprint.
import { openCapture } from "./storage.mjs"
import { emit } from "../lib/trace.mjs"
import { fingerprint, MEASURED_TOOLS, seenOf, turn } from "../lib/workload.mjs"
// CONTRACT_TS_CORE_DIR names a directory with a modified copy of ts-core: the negative controls of test/matrix.test.mjs.
const { Engine } = await import(process.env.CONTRACT_TS_CORE_DIR ? `${process.env.CONTRACT_TS_CORE_DIR}/engine.ts` : "./ts-core/engine.ts")

const argv = process.argv.slice(2)
const arg = (name, fallback) => { const i = argv.indexOf(`--${name}`); return i < 0 ? fallback : argv[i + 1] }
const flag = name => argv.includes(`--${name}`)
const db = arg("db"), turns = Number(arg("turns", "0")), tools = Number(arg("tools", String(MEASURED_TOOLS))), stub = arg("stub", "js")
if (flag("payload-kb")) process.env.CONTRACT_PAYLOAD_KB = arg("payload-kb")
if (flag("parallel") || arg("stream") || arg("fault") || Number(arg("seed", "0"))) { console.error("ts-driver: parallel, stream, fault and seed runs are outside the bench slice of the TS control"); process.exit(3) }

const cap = openCapture(db)
const asBlob = p => (p instanceof ArrayBuffer ? new Uint8Array(p) : p)
const store = {
	run: (q, ...p) => { cap.run(q, p.map(asBlob)) },
	all: (q, ...p) => cap.all(q, p.map(asBlob)),
	raw: (q, ...p) => cap.rows(q, p.map(asBlob)),
	tx: work => { cap.begin(); try { work() } catch (e) { cap.rollback(); throw e } cap.commit() },
}
const engine = new Engine(store, { stub, sidecar: flag("sidecar"), shape: "da866ada" })
let calls = 0
engine.fingerprints = []
engine.fingerprint = messages => { const fp = fingerprint(seenOf(messages)); emit({ t: "model", call: ++calls, bench: fp }); return fp }

if (flag("recover")) engine.recover()
for (let i = 0; i < turns; i++) { const t = turn(`${arg("label", "m")}${i}`, tools); engine.turn(t) }
cap.finish()
console.log(JSON.stringify({ turns, calls }))
