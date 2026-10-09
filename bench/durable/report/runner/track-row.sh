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
# differ on win or lose. Verdicts as the deck: lose when PiG is more than 3% worse, win from 1.3x. `summary` is one line.
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
make -s -C "$PIG_SOURCE/durable/core" build-tinygo >/dev/null
mkdir -p "$OUT"
run=$(mktemp -d "$OUT/run-$SHA-XXXX")
cd "$BENCH"
rm -rf results
# durable-bench's published seed fingerprints: the same history, or the row means nothing.
declare -A FINGERPRINT=([50]=b017b487524e44a4 [250]=dcea9f30b0917245 [1000]=ac520308146f2a8f [3500]=0a8c8e4b0d9a0794)
for t in pig-tinygo pi-head; do
	step taskset -c "$CPU" "$CLIENT" bench/seed.ts "$t" "$SIZE" 2>/dev/null
	cp "fixtures/$t-$SIZE.json" "$run/seed-$t.json"
	got=$(node -e 'process.stdout.write(JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).fingerprint)' "$run/seed-$t.json")
	[[ -z ${FINGERPRINT[$SIZE]:-} || $got == "${FINGERPRINT[$SIZE]}" ]] || { echo "track-row: $t at $SHA seeded fingerprint $got, want ${FINGERPRINT[$SIZE]}" >&2; exit 1; }
done
T=(pig-tinygo pi-head empty)
for ((s = 0; s < SAMPLES; s++)); do
	for i in 0 1 2; do SAMPLES=1 step taskset -c "$CPU" "$CLIENT" bench/run.ts "${T[$(((i + s) % 3))]}" "$SIZE" 2>/dev/null; done
done
for t in pig-tinygo pi-head; do BENCH_PROBE=count SAMPLES=1 step taskset -c "$CPU" "$CLIENT" bench/probe.ts "$t" "$SIZE" 2>/dev/null; done
mv results/results.jsonl results/probe.jsonl "$run/"
sha256sum "$PIG_SOURCE/durable/core/host/cf/dist/core-tinygo.wasm" >"$run/ENV.txt"
echo "load $(cut -d' ' -f1-3 /proc/loadavg)" >>"$run/ENV.txt"

node --input-type=module - "$run" "$SHA" "$SIZE" "$BASELINE" ${HISTORY:-"$OUT/track.jsonl"} <<'EOF' | tee -a "$OUT/track.jsonl"
import { existsSync, readFileSync } from "node:fs"
const [run, sha, size, baselinePath, ...historyFiles] = process.argv.slice(2)
const lines = (f) => readFileSync(`${run}/${f}`, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l))
const q = (xs, p) => { const s = [...xs].sort((a, b) => a - b); const k = (s.length - 1) * p, f = Math.floor(k), c = Math.min(f + 1, s.length - 1); return s[f] + (s[c] - s[f]) * (k - f) }
const med = (xs) => q(xs, 0.5)
const samples = lines("results.jsonl")
const of = (t) => samples.filter((x) => x.target === t)
const raw = { warm: (t) => of(t).flatMap((x) => x.turn.slice(1)), cold: (t) => of(t).map((x) => x.open + x.turn[0]) }
const floor = { warm: med(raw.warm("empty")), cold: med(raw.cold("empty")) }
const net = (kind, t) => raw[kind](t).map((v) => v - floor[kind])
const probes = lines("probe.jsonl")
const probeOf = (t) => probes.find((x) => x.target === t)
const p = probeOf("pig-tinygo")
const pp = probeOf("pi-head")
const seedOf = (t) => JSON.parse(readFileSync(`${run}/seed-${t}.json`, "utf8"))
const seed = seedOf("pig-tinygo")
const retained = (x) => (x.retained.heapUsed + x.retained.backing + x.retained.wasmBytes) / 1e6
const r = {
	warm: med(net("warm", "pig-tinygo")), warmP95: q(net("warm", "pig-tinygo"), 0.95), warmMax: Math.max(...net("warm", "pig-tinygo")),
	cold: med(net("cold", "pig-tinygo")), coldP95: q(net("cold", "pig-tinygo"), 0.95), coldMax: Math.max(...net("cold", "pig-tinygo")),
	seedS: seed.seedMs / 1000,
	cpuWarm: med(p.turn.slice(1).map((x) => x.cpuMs)), cpuCold: p.open.cpuMs + p.turn[0].cpuMs,
	retainedMB: retained(p), wasmMB: Math.max(...[p.open, ...p.turn].map((x) => x.wasmBytes)) / 1e6,
	crossings: med(p.turn.slice(1).map((x) => x.crossings)), rowsRead: med(p.turn.slice(1).map((x) => x.rowsRead)), rowsWritten: med(p.turn.slice(1).map((x) => x.rowsWritten)),
	piWarm: med(net("warm", "pi-head")), piWarmP95: q(net("warm", "pi-head"), 0.95), piWarmMax: Math.max(...net("warm", "pi-head")),
	piCold: med(net("cold", "pi-head")), piColdP95: q(net("cold", "pi-head"), 0.95),
	piSeedS: seedOf("pi-head").seedMs / 1000, piCpuWarm: med(pp.turn.slice(1).map((x) => x.cpuMs)), piCpuCold: pp.open.cpuMs + pp.turn[0].cpuMs,
	piRetainedMB: retained(pp), piRowsRead: med(pp.turn.slice(1).map((x) => x.rowsRead)), piRowsWritten: med(pp.turn.slice(1).map((x) => x.rowsWritten)),
	floorWarm: floor.warm, floorCold: floor.cold,
}
const base = JSON.parse(readFileSync(baselinePath, "utf8"))
const pct = (a, b) => (b ? `${a >= b ? "+" : ""}${(100 * (a - b) / b).toFixed(0)}%` : "n/a")
const round = (v) => Math.round(v * 10) / 10
const delta = Object.fromEntries(Object.keys(r).map((k) => [k, pct(r[k], base[k])]))
// PiG metric -> its pi control. Rolling: median of the pi values of the last 5 runs that have one, this run included.
const PAIRS = { warm: "piWarm", warmP95: "piWarmP95", cold: "piCold", coldP95: "piColdP95", cpuWarm: "piCpuWarm", cpuCold: "piCpuCold", retainedMB: "piRetainedMB", seedS: "piSeedS", rowsRead: "piRowsRead", rowsWritten: "piRowsWritten" }
const history = historyFiles.filter(existsSync).flatMap((f) => readFileSync(f, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l))).filter((h) => h.rule === "A" && h.turns === Number(size) && h.values)
history.sort((a, b) => a.at.localeCompare(b.at))
const verdict = (pig, pi) => (pig > pi * 1.03 ? "lose" : pi / pig >= 1.3 ? "win" : pig < pi * 0.97 ? "slightly better" : "tie")
const compare = {}
for (const [k, pk] of Object.entries(PAIRS)) {
	const past = history.map((h) => h.values[pk]).filter((v) => typeof v === "number").slice(-4)
	const rolling = med([...past, r[pk]])
	const a = verdict(r[k], r[pk])
	const b = verdict(r[k], rolling)
	compare[k] = { pig: round(r[k]), piRun: round(r[pk]), piRolling: round(rolling), nRolling: past.length + 1, run: a, rolling: b, disagree: (a === "win") !== (b === "win") || (a === "lose") !== (b === "lose") }
}
const disagree = Object.keys(compare).filter((k) => compare[k].disagree)
const NAMES = { warm: "warm p50", warmP95: "warm p95", cold: "cold p50", coldP95: "cold p95", cpuWarm: "CPU/warm turn", cpuCold: "CPU cold", retainedMB: "retained MB", seedS: "seed s", rowsRead: "rows read/turn", rowsWritten: "rows written/turn" }
const fmt = (v) => (Math.abs(v) >= 100 ? Math.round(v) : round(v)).toLocaleString("en-US")
const side = (c) => `${c.run}${c.disagree ? ` / rolling ${c.rolling}` : ""}`
const summary = `Rule A, TinyGo ${Number(size).toLocaleString("en-US")} turns at ${sha} (net of floor ${round(floor.warm)} ms; warm n=${raw.warm("pig-tinygo").length}). PiG vs pi same run (rolling median of last ${compare.warm.nRolling} pi): ${Object.entries(compare).map(([k, c]) => `${NAMES[k]} ${fmt(c.pig)} vs ${fmt(c.piRun)} (${fmt(c.piRolling)}) ${side(c)}`).join("; ")}. Also: warm max ${fmt(r.warmMax)}, Wasm high-water ${round(r.wasmMB)} MB, crossings ${fmt(r.crossings)}/turn; vs ${base.core}: warm p50 ${delta.warm}, cold ${delta.cold}, retained ${delta.retainedMB}. ${disagree.length ? `DISAGREE (same-run vs rolling pi): ${disagree.map((k) => NAMES[k]).join(", ")}.` : "Same-run and rolling pi agree on every win/lose."} fingerprint ${seed.fingerprint}. Private tracker, not deck numbers.`
console.log(JSON.stringify({ at: new Date().toISOString(), rule: "A", warmTurns: raw.warm("pig-tinygo").length, core: sha, turns: Number(size), fingerprint: seed.fingerprint, run, baseline: base.core, values: Object.fromEntries(Object.entries(r).map(([k, v]) => [k, round(v)])), delta, compare, disagree, summary }))
EOF
