#!/usr/bin/env bash
# The final benchmark session (RUNNER-SPEC.md section 3): seed every size from zero per target, then measure in interleaved
# rounds, with the native build measured by the core's bench --timing in the same rotation. Raw outputs go to $OUT; nothing
# is edited afterwards. Run it in one tmux session on the benchmark machine, after the CONTRACT gate is green.
#
#   BENCH=<durable-bench checkout, e9abffa + gate-cpus.patch + pig-targets.patch, node_modules installed>
#   PIG_SOURCE=<PiG checkout at the gated core>  PIG_VERSION=<core commit>  PI_SOURCE=<pi checkout at the reference>
#   NATIVE_BIN=<bench binary built from the gated core>  NATIVE_FIXTURES=<dir of pi-main-<n>.sqlite standard fixtures>
#   OUT=<output dir>  [TARGETS="pi-head tardie pig-tinygo pig-go pig-native empty"] [SIZES="50 250 1000 3500"] [ROUNDS=12]
#   [CPU=23] [PROBE_ROUNDS=3] [PROBE_SIZES=$SIZES: count probes] [PROBE_TIME_SIZES="50 3500"] [DRY=1: print the commands]
#   [CLIENT=bun: the runtime of durable-bench's scripts; node 24 runs them unchanged] [STEP_TIMEOUT=1800: seconds per step,
#   each step is tried up to 3 times and every retry is written to ENV.txt] [RESUME=1: keep the seeds already in $OUT]
#   PI_HEAD_VERSION=da866ada is required with pi-head; GATE_CPUS defaults to $CPU (gate-cpus.patch).
# `empty` (floor.patch) is the floor every Miniflare latency is reported net of; it has no history to seed. Each round
# measures 9 warm turns per target and size, so ROUNDS=12 gives the 108 the deck's p50/p95/max need (at least 100).
# After the latency rounds, the where-it-runs probe (section 4a, where-it-runs.patch) runs on the Durable Object targets:
# upload sizes, then PROBE_ROUNDS rotated rounds of BENCH_PROBE=count samples at every size (rows written, Wasm
# high-water mark) and BENCH_PROBE=time samples at PROBE_TIME_SIZES.
set -euo pipefail

TARGETS=${TARGETS:-"pi-head tardie pig-tinygo pig-go pig-native empty"}
SIZES=${SIZES:-"50 250 1000 3500"}
ROUNDS=${ROUNDS:-12}
CPU=${CPU:-23}
CLIENT=${CLIENT:-bun}
export GATE_CPUS=${GATE_CPUS:-$CPU}
PROBE_ROUNDS=${PROBE_ROUNDS:-3}
PROBE_SIZES=${PROBE_SIZES:-$SIZES}
PROBE_TIME_SIZES=${PROBE_TIME_SIZES:-"50 3500"}
((ROUNDS * 9 >= 100)) || echo "run-session: ROUNDS=$ROUNDS gives $((ROUNDS * 9)) warm turns per cell; a final deck needs 100" >&2
: "${BENCH:?}" "${OUT:?}"
case " $TARGETS " in *" pig-"*) : "${PIG_SOURCE:?}" "${PIG_VERSION:?}" ;; esac
case " $TARGETS " in *" pig-native "*) : "${NATIVE_BIN:?}" "${NATIVE_FIXTURES:?}" ;; esac
case " $TARGETS " in *" pi-head "*) : "${PI_HEAD_VERSION:?pi-head lines must carry the reference commit}" ;; esac
export PIG_SOURCE PIG_VERSION PI_SOURCE PI_HEAD_VERSION

# durable-bench's published seed fingerprints (workload.mjs SEED_FINGERPRINTS).
declare -A FINGERPRINT=([50]=b017b487524e44a4 [250]=dcea9f30b0917245 [1000]=ac520308146f2a8f [3500]=0a8c8e4b0d9a0794)

# rotate-by k list...: the list rotated left by k modulo its length.
rotate_by() {
	local k=$1; shift
	local items=("$@") n=$#
	for ((i = 0; i < n; i++)); do printf '%s\n' "${items[$(((i + k) % n))]}"; done
}

run() {
	if [[ ${DRY:-} == 1 ]]; then printf '%s\n' "$*"; return; fi
	local try
	for try in 1 2 3; do
		if timeout "${STEP_TIMEOUT:-1800}" "$@"; then return 0; fi
		echo "run-session: attempt $try failed: $*" >&2
		echo "retry $(date -u +%FT%TZ) attempt $try: $*" >>"$OUT/ENV.txt"
	done
	return 1
}

# Seedable targets: every durable-bench target but the floor. The native build has no seed time (RUNNER-SPEC "Native build").
seedable() { for t in $TARGETS; do [[ $t == pig-native || $t == empty ]] || printf '%s\n' "$t"; done; }

seed() { # seed <target> <size> <run>
	local t=$1 n=$2 k=$3
	(cd "$BENCH" && run taskset -c "$CPU" "$CLIENT" bench/seed.ts "$t" "$n")
	[[ ${DRY:-} == 1 ]] && return
	local got
	got=$(node -e 'process.stdout.write(JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).fingerprint)' "$BENCH/fixtures/$t-$n.json")
	if [[ -n ${FINGERPRINT[$n]:-} && $got != "${FINGERPRINT[$n]}" ]]; then
		echo "run-session: $t seeded $n turns with fingerprint $got, want ${FINGERPRINT[$n]}" >&2
		exit 1
	fi
	cp "$BENCH/fixtures/$t-$n.json" "$OUT/seeds/$t-$n-run$k.json"
}

measure() { # measure <target> <size> <round>
	local t=$1 n=$2 r=$3
	if [[ $t == pig-native ]]; then
		local db="<fresh dir>/s.sqlite"
		[[ ${DRY:-} == 1 ]] || db=$(mktemp -d)/s.sqlite
		run cp "$NATIVE_FIXTURES/pi-main-$n.sqlite" "$db"
		run taskset -c "$CPU" "$NATIVE_BIN" --db "$db" --timing "$OUT/native.jsonl" --version "$PIG_VERSION" --size "$n" --sample "$r" --turns 10 --tools 8
		[[ ${DRY:-} == 1 ]] || rm -rf "$(dirname "$db")"
	else
		(cd "$BENCH" && SAMPLES=1 run taskset -c "$CPU" "$CLIENT" bench/run.ts "$t" "$n")
	fi
}

# The Durable Object targets the where-it-runs probe compares (pi-durable main and the Wasm builds).
probed() { for t in $TARGETS; do case $t in pi-head | pig-tinygo | pig-go) printf '%s\n' "$t" ;; esac; done; }

for f in results.jsonl probe.jsonl size.jsonl; do
	if [[ ${DRY:-} != 1 && ${RESUME:-} != 1 && -e $BENCH/results/$f ]]; then
		echo "run-session: $BENCH/results/$f exists; the session's files hold this session only" >&2
		exit 1
	fi
done
mkdir -p "$OUT/seeds"
{
	echo "start $(date -u +%FT%TZ)"
	echo "host $(hostname) $(uname -sr)"
	lscpu | grep -E '^Model name' || true
	echo "client $CLIENT; node $(node --version) bun $(bun --version 2>/dev/null || echo none); STEP_TIMEOUT ${STEP_TIMEOUT:-1800}; GATE_CPUS $GATE_CPUS; CPU_MAX ${CPU_MAX:-default}"
	echo "targets $TARGETS; sizes $SIZES; rounds $ROUNDS; cpu $CPU; pig $PIG_VERSION"
	sha256sum "$(dirname "$0")"/*.patch
	[[ -z ${PIG_SOURCE:-} ]] || sha256sum "$PIG_SOURCE"/durable/core/host/cf/dist/core-*.wasm
	[[ -z ${NATIVE_BIN:-} ]] || sha256sum "$NATIVE_BIN"
	echo "load $(cut -d' ' -f1-3 /proc/loadavg)"
} >>"$OUT/ENV.txt"

# Seed, timed, from zero: one invocation per size, targets rotated per size.
i=0
for n in $SIZES; do
	i=$((i + 1))
	for t in $(rotate_by "$i" $(seedable)); do [[ ${RESUME:-} == 1 && -e $OUT/seeds/$t-$n-run1.json ]] || seed "$t" "$n" 1; done
done

# Measure, interleaved; the largest size is seeded twice more, between rounds, for the seed medians.
largest=${SIZES##* }
for ((r = 0; r < ROUNDS; r++)); do
	if ((ROUNDS >= 3 && (r == ROUNDS / 3 || r == 2 * ROUNDS / 3))); then
		k=$((r == ROUNDS / 3 ? 2 : 3))
		for t in $(rotate_by "$r" $(seedable)); do seed "$t" "$largest" "$k"; done
	fi
	for n in $SIZES; do
		for t in $(rotate_by "$r" $TARGETS); do measure "$t" "$n" "$r"; done
	done
done

# Where it runs: upload sizes of the latency bundles, then the probe, rotated per round, on the fixtures seeded above.
if [[ -n $(probed) ]]; then
	for t in $(probed); do (cd "$BENCH" && run "$CLIENT" bench/size.ts "$t"); done
	for ((r = 0; r < PROBE_ROUNDS; r++)); do
		for n in $PROBE_SIZES; do
			for t in $(rotate_by "$r" $(probed)); do
				for mode in count time; do
					[[ $mode == time && " $PROBE_TIME_SIZES " != *" $n "* ]] && continue
					(cd "$BENCH" && run env BENCH_PROBE=$mode SAMPLES=1 taskset -c "$CPU" "$CLIENT" bench/probe.ts "$t" "$n")
				done
			done
		done
	done
fi

if [[ ${DRY:-} != 1 ]]; then
	cp "$BENCH/results/results.jsonl" "$OUT/results.jsonl"
	[[ -z $(probed) ]] || cp "$BENCH/results/probe.jsonl" "$BENCH/results/size.jsonl" "$OUT/"
fi
{
	echo "end $(date -u +%FT%TZ)"
	echo "load $(cut -d' ' -f1-3 /proc/loadavg)"
} >>"$OUT/ENV.txt"
