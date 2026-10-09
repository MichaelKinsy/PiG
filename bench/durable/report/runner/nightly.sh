#!/usr/bin/env bash
# The nightly wall-clock run (plan 2 item 9): the newest integrated core, measured as the deck measures it, judged against
# the last nights (lib/nightly.mjs). Rule A is track-row.sh: PiG TinyGo at SIZE turns in Miniflare, pi-durable main and the
# empty floor in the same rotated rounds (108 warm turns), net of the floor. Rule B is the native build's own bench (its
# scripted model, no request path, no floor) on the same history: ROUNDS fresh processes of 10 turns, 108 warm turns.
# Appends the night to $OUT/nightly.jsonl and prints one line; the line starts with DRIFT when a metric is slower than its
# recent median by more than 10% while its ratio to pi-durable's same-run value grew by more than 10% too.
# The count gate (durable/contract/cli/counts.mjs) runs on every merge; this run is the timing that counts cannot show.
#
#   nightly.sh
#   BRANCH=<branch of the integrated core on the staging remote>  BENCH=<durable-bench with the runner patches>
#   PIG_SOURCE=<PiG worktree>  OUT=<dir>  BASELINE=<json file, as track-row.sh>  NATIVE_FIXTURES=<dir of pi-main-<n>.sqlite>
#   [CPU=23] [SIZE=3500] [ROUNDS=12] [HISTORY: as track-row.sh]
#   [CONTRACT_PI_SOURCE DURABLE_CONTRACT_CACHE: the contract environment, for time travel] [TT_IMPLS=wasm-tinygo,native] [TT_RUNS=5]
set -euo pipefail
: "${BRANCH:?}" "${BENCH:?}" "${PIG_SOURCE:?}" "${OUT:?}" "${BASELINE:?}" "${NATIVE_FIXTURES:?}"
CPU=${CPU:-23}
SIZE=${SIZE:-3500}
ROUNDS=${ROUNDS:-12}
here=$(cd "$(dirname "$0")" && pwd)
# track-row.sh reads these from its environment: a value set in the caller's shell but not exported would pass the check above.
export BENCH PIG_SOURCE BASELINE

git -C "$PIG_SOURCE" fetch -q staging
SHA=$(git -C "$PIG_SOURCE" rev-parse --short=10 "staging/$BRANCH")
night="$OUT/nights/$(date -u +%Y%m%dT%H%M%SZ)-$SHA"
mkdir -p "$night"

# Rule A: track-row.sh checks the core out, builds it, seeds from zero and runs the rounds under $OUT/.lock.
CPU=$CPU SIZE=$SIZE OUT="$OUT" bash "$here/track-row.sh" "$SHA" > "$night/rule-a.out" 2> "$night/rule-a.err"

# Rule B under the same lock: nothing else uses the core checkout or the CPU meanwhile.
exec 9>"$OUT/.lock"
flock 9
(cd "$PIG_SOURCE" && git checkout -q --detach "$SHA" && sh durable/core/budget/build-native.sh > "$night/native-build.log" 2>&1)
bin="$PIG_SOURCE/durable/core/sqlhost/bin/bench"
for r in $(seq 0 $((ROUNDS - 1))); do
	db="$night/native-$r.sqlite"
	for x in "" -wal; do [[ ! -f "$NATIVE_FIXTURES/pi-main-$SIZE.sqlite$x" ]] || cp "$NATIVE_FIXTURES/pi-main-$SIZE.sqlite$x" "$db$x"; done
	taskset -c "$CPU" "$bin" --db "$db" --timing "$night/native.jsonl" --version "$SHA" --size "$SIZE" --sample "$r" --turns 10 --tools 8 > /dev/null
	rm -f "$db" "$db-wal" "$db-shm"
done

# Time travel, when the core has robust-durable's scenarios/time-travel.mjs and the contract environment is set
# (CONTRACT_PI_SOURCE, DURABLE_CONTRACT_CACHE): a fork at turn 100, as-of reads, snapshotAsOf and an 8-point scrub on the
# SIZE-turn history; pi first, each build must equal it. TT_RUNS runs (about 10 s each at 3,500 turns); each operation's
# median is judged night to night against pi's same operation. A failing build is reported in the line and does not stop
# the night.
if [[ -f "$PIG_SOURCE/durable/contract/scenarios/time-travel.mjs" && -n "${CONTRACT_PI_SOURCE:-}" && -n "${DURABLE_CONTRACT_CACHE:-}" ]]; then
	for r in $(seq 1 "${TT_RUNS:-5}"); do
		(cd "$PIG_SOURCE" && taskset -c "$CPU" node durable/contract/cli/robust.mjs timetravel --impls "${TT_IMPLS:-wasm-tinygo,native}" --size "$SIZE" --out "$night/timetravel" --json "$night/timetravel-$r.json" > "$night/timetravel-$r.txt" 2>&1) || true
	done
fi

node "$here/nightly-record.mjs" "$night" "$OUT/nightly.jsonl" "$SHA"
