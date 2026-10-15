// The nightly drift judgement (lib/nightly.mjs): a slower night drifts only when its ratio to pi-durable's same-run value
// grew too, a load spike that slows pi as much does not, and fewer than three earlier nights judge nothing.
import assert from "node:assert/strict"
import test from "node:test"
import { describeNight, judge, quantile, ruleB, timeTravel } from "../lib/nightly.mjs"

const night = (warm, piWarm, extra = {}) => ({ pig: { warm, warmP95: warm * 1.5, cold: warm * 5, ...extra }, pi: { warm: piWarm, warmP95: piWarm * 1.5, cold: piWarm * 3 } })
const history = [night(200, 260), night(210, 270), night(205, 265), night(195, 255)]

test("ruleB pools every turn after the first as warm, and open + first turn as cold", () => {
	const r = ruleB([{ open: 5, turn: [100, 10, 20] }, { open: 7, turn: [90, 30, 40] }])
	assert.equal(r.nativeWarmTurns, 4)
	assert.equal(r.nativeWarm, 25)
	assert.equal(r.nativeCold, 101)
	assert.equal(quantile([1, 2, 3, 4, 5], 0.95), 4.8)
})

test("a night slower than its median and slower against pi drifts", () => {
	const v = judge(history, night(240, 262))
	const warm = v.find(x => x.key === "warm")
	assert.equal(warm.drift, true)
	assert.ok(warm.slower > 0.1 && warm.ratioUp > 0.1)
	assert.match(describeNight("abc", v), /^DRIFT Rule A warm p50, Rule A warm p95, Rule A cold p50\. abc: Rule A warm p50 240 ms \(median 203, \+19%; vs pi \+19%\)/)
})

test("a load spike that slows pi as much is not drift", () => {
	const v = judge(history, night(240, 315))
	assert.equal(v.find(x => x.key === "warm").drift, false)
	assert.match(describeNight("abc", v), /^no drift\./)
})

test("within 10% is not drift, and fewer than three earlier nights judge nothing", () => {
	assert.equal(judge(history, night(220, 262)).some(x => x.drift), false)
	const early = judge(history.slice(0, 2), night(400, 260))
	assert.equal(early.some(x => x.drift), false)
	assert.equal(early[0].nights, 2)
	assert.match(describeNight("abc", early), /2 earlier nights, not judged/)
})

test("Rule B is judged against its own nights with pi's warm p50 as the load control", () => {
	const h = history.map(n => ({ ...n, pig: { ...n.pig, nativeWarm: 100 } }))
	assert.equal(judge(h, night(200, 262, { nativeWarm: 120 })).find(x => x.key === "nativeWarm").drift, true)
	assert.equal(judge(h, night(200, 330, { nativeWarm: 120 })).find(x => x.key === "nativeWarm").drift, false)
})

test("a ratio that grew because pi got faster is not drift while PiG holds its own median", () => {
	// PiG at its median (203), pi 20% faster than usual: the ratio rises by about 25%, PiG is not slower.
	const v = judge(history, night(203, 210)).find(x => x.key === "warm")
	assert.ok(v.ratioUp > 0.1 && v.slower < 0.1, JSON.stringify(v))
	assert.equal(v.drift, false)
})

test("only the last five nights count, and their median, not their mean", () => {
	// Two old slow nights fall outside the window; one slow night inside it moves the mean past 10% but not the median.
	const old = [night(400, 260), night(400, 260)]
	const recent = [night(200, 260), night(200, 260), night(200, 260), night(200, 260), night(600, 260)]
	const v = judge([...old, ...recent], night(225, 260)).find(x => x.key === "warm")
	assert.equal(v.nights, 5)
	assert.equal(v.median, 200)
	assert.equal(v.drift, true)
})

test("Rule B's load control is pi's warm p50, not its cold start", () => {
	// pi's warm p50 rose as much as native's (a load spike), its cold start did not: no drift.
	const h = history.map(n => ({ ...n, pig: { ...n.pig, nativeWarm: 100 } }))
	const spike = { pig: { nativeWarm: 125 }, pi: { warm: 330, warmP95: 400, cold: 780 } }
	assert.equal(judge(h, spike).find(x => x.key === "nativeWarm").drift, false)
})

test("time travel: per-operation sums, pi as each operation's control, failed builds reported without timings", () => {
	const r = {
		runs: {
			pi: { ok: true, ops: [{ op: "fork", ms: 10 }, { op: "scrub", ms: 2 }, { op: "scrub", ms: 3 }] },
			"wasm-tinygo": { ok: false, problem: "exited 1 : FatalError: trap", ops: [{ op: "fork", ms: 9 }] },
			native: { ok: true, ops: [{ op: "fork", ms: 20 }, { op: "scrub", ms: 4 }, { op: "scrub", ms: 4 }] },
		},
	}
	const tt = timeTravel(r)
	assert.deepEqual(tt.pi, { "tt:fork": 10, "tt:scrub": 5 })
	assert.deepEqual(tt.pig, { "tt:native:fork": 20, "tt:native:scrub": 8 })
	assert.deepEqual(tt.verdicts, { "wasm-tinygo": "exited 1 : FatalError: trap", native: "ok" })
	const withTT = n => ({ pig: { ...n.pig, ...tt.pig }, pi: { ...n.pi, ...tt.pi } })
	const h = history.map(withTT)
	// The native fork slows 50% while pi's fork holds: drift. The same slowdown in pi's fork too: none.
	const slow = withTT(night(200, 262)); slow.pig["tt:native:fork"] = 30
	assert.equal(judge(h, slow).find(v => v.key === "tt:native:fork").drift, true)
	const load = withTT(night(200, 262)); load.pig["tt:native:fork"] = 30; load.pi["tt:fork"] = 15
	assert.equal(judge(h, load).find(v => v.key === "tt:native:fork").drift, false)
	assert.match(describeNight("abc", judge(h, slow), tt.verdicts), /DRIFT time travel native fork\. .* Time travel: wasm-tinygo FAIL \(exited 1 : FatalError: trap\)\.$/)
	assert.match(describeNight("abc", judge(h, slow), { native: "ok" }), /Time travel: equal to pi on native\.$/)
	// A 2 ms operation that doubles is timer noise, not drift.
	const tiny = withTT(night(200, 262)); tiny.pig["tt:native:scrub"] = 16
	const ht = h.map(n => ({ ...n, pig: { ...n.pig, "tt:native:scrub": 12 } }))
	assert.equal(judge(ht, tiny).find(v => v.key === "tt:native:scrub").drift, false)
})

test("time travel over several runs: medians per operation, a build failing in any run is reported and untimed", () => {
	const run = (fork, ok = true) => ({ runs: { pi: { ok: true, ops: [{ op: "fork", ms: 10 }] }, native: { ok: true, ops: [{ op: "fork", ms: fork }] }, "wasm-go": { ok, problem: ok ? undefined : "differs from pi", ops: [{ op: "fork", ms: 5 }] } } })
	// The median (22) is neither the last run (90) nor the mean (44); the failure is in a later run, not the first.
	const tt = timeTravel([run(20), run(22, false), run(90)])
	assert.deepEqual(tt.pig, { "tt:native:fork": 22 })
	assert.deepEqual(tt.pi, { "tt:fork": 10 })
	assert.deepEqual(tt.verdicts, { native: "ok", "wasm-go": "differs from pi" })
})

test("a time-travel run that wrote no result is named in the line", () => {
	const line = describeNight("abc", judge(history, night(200, 262)), { native: "ok" }, ["timetravel-2.txt"])
	assert.match(line, /Time travel: equal to pi on native\. Time travel: 1 run\(s\) gave no result \(timetravel-2\.txt\)\.$/)
	// Every run lost: the line still says so.
	assert.match(describeNight("abc", [], undefined, ["timetravel-1.txt", "timetravel-2.txt"]), /2 run\(s\) gave no result/)
})
