#!/usr/bin/env bash
# Tracks one deck row between full sessions: PiG Durable (TinyGo) at 3,500 turns on a given core commit, with pi-durable
# main measured in the same run as the noise control and the empty target as the floor (floor.patch). Seeds the history
# from zero (fingerprint checked), then SAMPLES rotated latency rounds and one count probe with the retained-memory reading
# (where-it-runs.patch). Reports as the deck does (RUNNER-SPEC 2a, all Rule A): warm p50/p95/max over every turn after
# the first, cold over the runtimes, both net of the floor's p50. Prints one JSON line with the values and the deltas
# against BASELINE (a JSON object of the same fields), and appends it to $OUT/track.jsonl.
# pi-durable main gets the same measurements in every run (seed from zero, count probe), so every PiG metric has a pi value
# from the same run. pi's own numbers drift on a shared host, so each PiG metric is also judged against the rolling median of
# the pi values of the last 5 runs (this one included) in HISTORY; `disagree` lists the metrics where the two verdicts
# differ on win or lose. Verdicts: inside the metric's A/A floor (below) "no measurable difference"; outside it lose when PiG
# is worse, win from 1.3x, else slightly better. `summary` is one line.
# Every arm has an A/A copy (results-integrity rule): each round runs PiG, pi, a second PiG and a second pi sample and the
# floor, in rotated order; each arm is seeded twice and probed twice. The A/A floor of a metric is the larger |ln(copy/arm)|
# of PiG and pi, and a PiG/pi difference inside it is "no measurable difference". The summary names the rounds, the raw
# samples ($run/results.jsonl, $run/probe.jsonl), the host facts (host, cores, load, governor) and the tree.
# These numbers are a private tracker, not deck numbers: the deck renders only from a full session (RUNNER-SPEC).
#
#   track-row.sh <core commit>
#   BENCH=<durable-bench with all runner patches>  PIG_SOURCE=<PiG worktree>  OUT=<dir>  BASELINE=<json file>
#   [PI_SOURCE=../pi] [PI_HEAD_VERSION=da866ada] [CPU=23] [SAMPLES=12: 108 warm turns] [SIZE=3500] [CLIENT=node]
#   [STEP_TIMEOUT=2400] [HISTORY="$OUT/track.jsonl": earlier runs, one or more files, for the rolling pi control]
set -euo pipefail
SHA=${1:?core commit}
: "${BENCH:?}" "${PIG_SOURCE:?}" "${OUT:?}" "${BASELINE:?}"
CPU=${CPU:-23}
SAMPLES=${SAMPLES:-12}
SIZE=${SIZE:-3500}
CLIENT=${CLIENT:-node}
export PI_SOURCE=${PI_SOURCE:-../pi} PI_HEAD_VERSION=${PI_HEAD_VERSION:-da866ada} GATE_CPUS=${GATE_CPUS:-$CPU} CPU_MAX=${CPU_MAX:-1}
export PIG_SOURCE PIG_VERSION=$SHA

# One run at a time per OUT: runs share BENCH's fixtures and PIG_SOURCE's checkout.
mkdir -p "$OUT"
exec 9>"$OUT/.lock"
flock 9

step() { local try; for try in 1 2 3; do timeout "${STEP_TIMEOUT:-2400}" "$@" && return 0; echo "track-row: attempt $try failed: $*" >&2; done; return 1; }

(cd "$PIG_SOURCE" && git fetch -q staging && git checkout -q --detach "$SHA")
SHA=$(cd "$PIG_SOURCE" && git rev-parse --short=10 HEAD)
export PIG_VERSION=$SHA
(cd "$PIG_SOURCE/durable/core/host/cf" && npm ci --silent >/dev/null 2>&1)
# Each tree builds with the TinyGo its own automation/dev/toolchains.lock pins (build-wasm.sh refuses any other version), so
# a candidate on an older or newer pin than the caller's TinyGo is measured, not failed. A tree without the installer
# builds with the caller's TINYGO or PATH.
if [[ -f "$PIG_SOURCE/durable/core/budget/tinygo-install.sh" ]]; then
	TINYGO=$(sh "$PIG_SOURCE/durable/core/budget/tinygo-install.sh" | tail -1)
	export TINYGO
fi
# The release that built the measured module goes into ENV.txt and the JSON line: two trees on different pins measure
# different compilers, so a delta against a baseline built with another release is not the core's own change.
TINYGO_VERSION=$("${TINYGO:-tinygo}" version | sed -n 's/^tinygo version \([^ ]*\) .*/\1/p')
export TINYGO_VERSION
make -s -C "$PIG_SOURCE/durable/core" build-tinygo >/dev/null
mkdir -p "$OUT"
run=$(mktemp -d "$OUT/run-$SHA-XXXX")
cd "$BENCH"
rm -rf results
# durable-bench's published seed fingerprints: the same history, or the row means nothing.
declare -A FINGERPRINT=([50]=b017b487524e44a4 [250]=dcea9f30b0917245 [1000]=ac520308146f2a8f [3500]=0a8c8e4b0d9a0794)
hostfacts() { printf '{"at":"%s","host":"%s","cpuModel":"%s","cores":%s,"pinnedCpu":"%s","governor":"%s","load":"%s"}\n' "$(date -u +%FT%TZ)" "$(hostname)" "$(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | sed 's/^ //')" "$(nproc --all)" "$CPU" "$(cat "/sys/devices/system/cpu/cpu${CPU%%[,-]*}/cpufreq/scaling_governor" 2>/dev/null || echo "none (no cpufreq driver)")" "$(cut -d' ' -f1-3 /proc/loadavg)"; }
hostfacts >"$run/host.jsonl"
# tagged <file> <arm>: copies the line the last step appended to results/<file> into $run/<file>, tagged with its arm.
tagged() { tail -1 "results/$1" | node -e 'let s = ""; process.stdin.on("data", (d) => (s += d)).on("end", () => console.log(JSON.stringify({ arm: process.argv[1], ...JSON.parse(s) })))' "$2" >>"$run/$1"; }
for arm in pig-tinygo pi-head pig-tinygo-aa pi-head-aa; do
	t=${arm%-aa}
	step taskset -c "$CPU" "$CLIENT" bench/seed.ts "$t" "$SIZE" 2>/dev/null
	cp "fixtures/$t-$SIZE.json" "$run/seed-$arm.json"
	got=$(node -e 'process.stdout.write(JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).fingerprint)' "$run/seed-$arm.json")
	[[ -z ${FINGERPRINT[$SIZE]:-} || $got == "${FINGERPRINT[$SIZE]}" ]] || { echo "track-row: $arm at $SHA seeded fingerprint $got, want ${FINGERPRINT[$SIZE]}" >&2; exit 1; }
done
A=(pig-tinygo pi-head pig-tinygo-aa pi-head-aa empty)
for ((s = 0; s < SAMPLES; s++)); do
	for i in 0 1 2 3 4; do
		arm=${A[$(((i + s) % 5))]}
		SAMPLES=1 step taskset -c "$CPU" "$CLIENT" bench/run.ts "${arm%-aa}" "$SIZE" 2>/dev/null
		tagged results.jsonl "$arm"
	done
done
for arm in pig-tinygo pi-head pig-tinygo-aa pi-head-aa; do
	BENCH_PROBE=count SAMPLES=1 step taskset -c "$CPU" "$CLIENT" bench/probe.ts "${arm%-aa}" "$SIZE" 2>/dev/null
	tagged probe.jsonl "$arm"
done
rm -f results/results.jsonl results/probe.jsonl
sha256sum "$PIG_SOURCE/durable/core/host/cf/dist/core-tinygo.wasm" >"$run/ENV.txt"
echo "tinygo $TINYGO_VERSION" >>"$run/ENV.txt"
hostfacts >>"$run/host.jsonl"

node --input-type=module - "$run" "$SHA" "$SIZE" "$BASELINE" "$SAMPLES" ${HISTORY:-"$OUT/track.jsonl"} <<'EOF' | tee -a "$OUT/track.jsonl"
import { existsSync, readFileSync } from "node:fs"
const [run, sha, size, baselinePath, rounds, ...historyFiles] = process.argv.slice(2)
const lines = (f) => readFileSync(`${run}/${f}`, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l))
const q = (xs, p) => { const s = [...xs].sort((a, b) => a - b); const k = (s.length - 1) * p, f = Math.floor(k), c = Math.min(f + 1, s.length - 1); return s[f] + (s[c] - s[f]) * (k - f) }
const med = (xs) => q(xs, 0.5)
const samples = lines("results.jsonl")
const of = (arm) => samples.filter((x) => x.arm === arm)
const raw = { warm: (t) => of(t).flatMap((x) => x.turn.slice(1)), cold: (t) => of(t).map((x) => x.open + x.turn[0]) }
const floor = { warm: med(raw.warm("empty")), cold: med(raw.cold("empty")) }
const net = (kind, t) => raw[kind](t).map((v) => v - floor[kind])
const probes = lines("probe.jsonl")
const probeOf = (arm) => probes.find((x) => x.arm === arm)
const seedOf = (arm) => JSON.parse(readFileSync(`${run}/seed-${arm}.json`, "utf8"))
const seed = seedOf("pig-tinygo")
const retained = (x) => (x.retained.heapUsed + x.retained.backing + x.retained.wasmBytes) / 1e6
// values(pig, pi): every metric for one PiG arm and one pi arm; the A/A copies are values("pig-tinygo-aa", "pi-head-aa").
const values = (pig, pi) => { const p = probeOf(pig), pp = probeOf(pi); return {
	warm: med(net("warm", pig)), warmP95: q(net("warm", pig), 0.95), warmMax: Math.max(...net("warm", pig)),
	cold: med(net("cold", pig)), coldP95: q(net("cold", pig), 0.95), coldMax: Math.max(...net("cold", pig)),
	seedS: seedOf(pig).seedMs / 1000,
	cpuWarm: med(p.turn.slice(1).map((x) => x.cpuMs)), cpuCold: p.open.cpuMs + p.turn[0].cpuMs,
	retainedMB: retained(p), wasmMB: Math.max(...[p.open, ...p.turn].map((x) => x.wasmBytes)) / 1e6,
	crossings: med(p.turn.slice(1).map((x) => x.crossings)), rowsRead: med(p.turn.slice(1).map((x) => x.rowsRead)), rowsWritten: med(p.turn.slice(1).map((x) => x.rowsWritten)),
	piWarm: med(net("warm", pi)), piWarmP95: q(net("warm", pi), 0.95), piWarmMax: Math.max(...net("warm", pi)),
	piCold: med(net("cold", pi)), piColdP95: q(net("cold", pi), 0.95),
	piSeedS: seedOf(pi).seedMs / 1000, piCpuWarm: med(pp.turn.slice(1).map((x) => x.cpuMs)), piCpuCold: pp.open.cpuMs + pp.turn[0].cpuMs,
	piRetainedMB: retained(pp), piRowsRead: med(pp.turn.slice(1).map((x) => x.rowsRead)), piRowsWritten: med(pp.turn.slice(1).map((x) => x.rowsWritten)),
	floorWarm: floor.warm, floorCold: floor.cold,
} }
const r = values("pig-tinygo", "pi-head")
const aa = values("pig-tinygo-aa", "pi-head-aa")
const base = JSON.parse(readFileSync(baselinePath, "utf8"))
const pct = (a, b) => (b ? `${a >= b ? "+" : ""}${(100 * (a - b) / b).toFixed(0)}%` : "n/a")
const round = (v) => Math.round(v * 10) / 10
const delta = Object.fromEntries(Object.keys(r).map((k) => [k, pct(r[k], base[k])]))
const tinygo = process.env.TINYGO_VERSION
// A baseline from before this field names no release; one that names another release gets its deltas labelled.
const crossToolchain = base.tinygo !== undefined && base.tinygo !== tinygo
// PiG metric -> its pi control. Rolling: median of the pi values of the last 5 runs that have one, this run included.
const PAIRS = { warm: "piWarm", warmP95: "piWarmP95", cold: "piCold", coldP95: "piColdP95", cpuWarm: "piCpuWarm", cpuCold: "piCpuCold", retainedMB: "piRetainedMB", seedS: "piSeedS", rowsRead: "piRowsRead", rowsWritten: "piRowsWritten" }
const history = historyFiles.filter(existsSync).flatMap((f) => readFileSync(f, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l))).filter((h) => h.rule === "A" && h.turns === Number(size) && h.values)
history.sort((a, b) => a.at.localeCompare(b.at))
// The A/A floor of a metric: the larger |ln(copy/arm)| of PiG and pi (0 when both are equal, as for row counts).
const lnr = (a, b) => (a === b ? 0 : Math.abs(Math.log(a / b)))
const verdict = (pig, pi, floor) => (lnr(pig, pi) <= floor ? "no measurable difference" : pig > pi ? "lose" : pi / pig >= 1.3 ? "win" : "slightly better")
const compare = {}
for (const [k, pk] of Object.entries(PAIRS)) {
	const past = history.map((h) => h.values[pk]).filter((v) => typeof v === "number").slice(-4)
	const rolling = med([...past, r[pk]])
	const floorK = Math.max(lnr(aa[k], r[k]), lnr(aa[pk], r[pk]))
	const a = verdict(r[k], r[pk], floorK)
	const b = verdict(r[k], rolling, floorK)
	compare[k] = { pig: round(r[k]), piRun: round(r[pk]), piRolling: round(rolling), nRolling: past.length + 1, aaPig: Math.round((aa[k] / r[k]) * 100) / 100, aaPi: Math.round((aa[pk] / r[pk]) * 100) / 100, floorPct: Math.round(100 * (Math.exp(floorK) - 1)), run: a, rolling: b, disagree: (a === "win") !== (b === "win") || (a === "lose") !== (b === "lose") }
}
const disagree = Object.keys(compare).filter((k) => compare[k].disagree)
const NAMES = { warm: "warm p50", warmP95: "warm p95", cold: "cold p50", coldP95: "cold p95", cpuWarm: "CPU/warm turn", cpuCold: "CPU cold", retainedMB: "retained MB", seedS: "seed s", rowsRead: "rows read/turn", rowsWritten: "rows written/turn" }
const fmt = (v) => (Math.abs(v) >= 100 ? Math.round(v) : round(v)).toLocaleString("en-US")
const side = (c) => `${c.run}${c.disagree ? ` / rolling ${c.rolling}` : ""}`
const hosts = lines("host.jsonl"), h = hosts[0]
const hostFacts = `host ${h.host}, ${h.cpuModel}, ${h.cores} cores, pinned CPU ${h.pinnedCpu}, governor ${h.governor}, load ${hosts.map((x) => x.load).join(" -> ")} (start, end)`
const summary = `Rule A, TinyGo ${Number(size).toLocaleString("en-US")} turns at tree ${sha}; ${rounds} interleaved measured rounds of PiG, pi, their A/A copies and the floor, rotated, one fresh process per sample (net of floor ${round(floor.warm)} ms; warm n=${raw.warm("pig-tinygo").length} and cold n=${raw.cold("pig-tinygo").length} per arm; CPU, retained MB and rows from ${probes.filter((x) => x.arm === "pig-tinygo").length} count probe and seed s from 1 seed per arm, each against its A/A copy); ${hostFacts}; raw samples ${run}/results.jsonl, probes ${run}/probe.jsonl. PiG vs pi same run (rolling median of last ${compare.warm.nRolling} pi) [A/A PiG, pi; floor]: ${Object.entries(compare).map(([k, c]) => `${NAMES[k]} ${fmt(c.pig)} vs ${fmt(c.piRun)} (${fmt(c.piRolling)}) [${c.aaPig}x, ${c.aaPi}x; ${c.floorPct}%] ${side(c)}`).join("; ")}. Also: warm max ${fmt(r.warmMax)}, Wasm high-water ${round(r.wasmMB)} MB, crossings ${fmt(r.crossings)}/turn; TinyGo ${tinygo}; vs ${base.core}${crossToolchain ? ` (built with TinyGo ${base.tinygo}: these deltas cross toolchains)` : ""}: warm p50 ${delta.warm}, cold ${delta.cold}, retained ${delta.retainedMB}. ${disagree.length ? `DISAGREE (same-run vs rolling pi): ${disagree.map((k) => NAMES[k]).join(", ")}.` : "Same-run and rolling pi agree on every win/lose."} fingerprint ${seed.fingerprint}. Private tracker, not deck numbers.`
console.log(JSON.stringify({ at: new Date().toISOString(), rule: "A", rounds: Number(rounds), host: h, aa: Object.fromEntries(Object.entries(aa).map(([k, v]) => [k, round(v)])), warmTurns: raw.warm("pig-tinygo").length, core: sha, tinygo, crossToolchain, turns: Number(size), fingerprint: seed.fingerprint, run, baseline: base.core, values: Object.fromEntries(Object.entries(r).map(([k, v]) => [k, round(v)])), delta, compare, disagree, summary }))
EOF
