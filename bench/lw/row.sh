#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# The LW benchmark rows: Pi-Bolt's own bench tools (bolt-v0.8.5) run against pig (built from this tree) and the Pi-Bolt 0.8.5
# release, interleaved, with every arm twice per round (A/A: two replicas of the same binary, so the table shows the noise
# beside the difference), for N rounds. Each tool invocation runs under the global bench lock. Raw logs and one normalized
# JSONL go to tmp/lw-bench/<wave>/, then bench/lw/rows.py prints the table and checks the ratio ceilings
# (lw.row.*.ratio_vs_bolt in automation/perf/ratchet.toml).
#
#   bench/lw/row.sh [--wave W] [--rounds N] [--rows large_write,long_answer,interactive,long_session,headless,startup] [--quick]
#                   [--pig PATH] [--out DIR] [--no-check]
#
#   large_write   large_write.py --sizes 50,200            pig -p writing a 50 / 200 KB file through 16-character argument deltas
#   long_answer   long_answer.py --sizes 20000,60000       Markdown answers streamed into the TUI at a fast model's pace
#   interactive   benchmark.py --scenarios interactive     TUI start, five turns, exit
#   long_session  long_session.py --prompts 75 --every 25  one TUI process, 75 prompts (about 4.2M tokens)
#   headless      benchmark.py --scenarios headless        pig -p: one prompt, four read tool calls on 47-239 KB files, the answer
#   startup       benchmark.py --scenarios startup         pig --version
#
# The default --rows are the first four; headless and startup are run by name.
# interactive, headless and startup also record each run's peak RSS (benchmark.py's ru_maxrss, as peak_rss_mb in runs.jsonl,
# for pig and Pi-Bolt alike); rows.py check holds the headless and interactive ratios on linux.
#
# --quick shrinks every row for a smoke run (50 KB, 5,000 characters, 10 prompts). --pig takes a built pig instead of
# building ./cmd/pig. The rounds rotate the order of the arms.
#
# Environment:
#   LW_BENCH_LOCK      command prefix that runs a command under the global bench lock (default: the LW fan-out's
#                      `lw-fanout.sh lock` when it exists, else a lock directory, LW_BENCH_LOCKDIR or /tmp/lw-bench.lock)
#   LW_BENCH_CACHE     where the Pi-Bolt checkout and release are kept (default ~/.cache/pig-lw-bench)
#   LW_BENCH_MAX_LOAD  when set, wait before each invocation until the 1-minute load average is below it
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BOLT_TAG=bolt-v0.8.5
BOLT_COMMIT=b93895d22a54d9efc4f6e14128bf2084b5f54f6f
BOLT_URL=https://github.com/opensec-git/Pi-Bolt
# sha256 of the release archives (the release's SHA256SUMS and GitHub's asset digests agree).
bolt_sha256() {
	case $1 in
	darwin-arm64) echo 7160704938a67df94231f8bf992a64f5071763eefdf705b5df8c20baadfdec93 ;;
	linux-x64) echo 3df3b1769f84d942da11e1b42988199d02a4233b5ae48034a1bd872208f5c8c8 ;;
	esac
}

WAVE=adhoc ROUNDS=12 ROWS=large_write,long_answer,interactive,long_session QUICK=0 PIG='' OUT='' CHECK=1
while [[ $# -gt 0 ]]; do
	case $1 in
	--wave) WAVE=$2; shift 2 ;;
	--rounds) ROUNDS=$2; shift 2 ;;
	--rows) ROWS=$2; shift 2 ;;
	--quick) QUICK=1; shift ;;
	--pig) PIG=$2; shift 2 ;;
	--out) OUT=$2; shift 2 ;;
	--no-check) CHECK=0; shift ;;
	-h | --help) sed -n '4,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "row.sh: unknown argument $1" >&2; exit 2 ;;
	esac
done
OUT=${OUT:-$ROOT/tmp/lw-bench/$WAVE}
CACHE=${LW_BENCH_CACHE:-$HOME/.cache/pig-lw-bench}
mkdir -p "$OUT/raw" "$OUT/bin" "$OUT/homes" "$CACHE"
OUT="$(cd "$OUT" && pwd)"

die() { echo "row.sh: $*" >&2; exit 1; }
log() { echo "row.sh: $*" >&2; }

case "$(uname -s)-$(uname -m)" in
Darwin-arm64) PLATFORM=darwin-arm64 ;;
Linux-x86_64) PLATFORM=linux-x64 ;;
*) die "no Pi-Bolt 0.8.5 release for $(uname -s)-$(uname -m)" ;;
esac

sha256() { if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
load1() { if [[ -r /proc/loadavg ]]; then cut -d' ' -f1 /proc/loadavg; else sysctl -n vm.loadavg | awk '{print $2}'; fi; }

# The global bench lock: one benchmark process at a time on the host.
FANOUT=$HOME/PiG-launch-newer/lead-tern/lw/lw-fanout.sh
if [[ -n ${LW_BENCH_LOCK:-} ]]; then
	read -r -a LOCK <<<"$LW_BENCH_LOCK"
elif [[ -x $FANOUT ]]; then
	LOCK=("$FANOUT" lock)
else
	LOCK=(lock_dir)
fi
lock_dir() {
	local dir=${LW_BENCH_LOCKDIR:-/tmp/lw-bench.lock} waited=0
	until mkdir "$dir" 2>/dev/null; do
		if [[ -f $dir/pid ]] && ! kill -0 "$(cat "$dir/pid")" 2>/dev/null; then rm -rf "$dir"; continue; fi
		((waited++ % 30 == 0)) && log "waiting for the bench lock held by pid $(cat "$dir/pid" 2>/dev/null)"
		sleep 2
	done
	echo $$ >"$dir/pid"
	local status=0
	"$@" || status=$?
	rm -rf "$dir"
	return $status
}

# Pi-Bolt's bench tools at the release commit.
HARNESS=$CACHE/Pi-Bolt-$BOLT_COMMIT
if [[ ! -f $HARNESS/bench/large_write.py ]]; then
	log "fetching Pi-Bolt $BOLT_TAG"
	rm -rf "$HARNESS.tmp"
	git -c advice.detachedHead=false clone -q --depth 1 --branch "$BOLT_TAG" "$BOLT_URL.git" "$HARNESS.tmp"
	[[ $(git -C "$HARNESS.tmp" rev-parse HEAD) == "$BOLT_COMMIT" ]] || die "$BOLT_TAG is not $BOLT_COMMIT"
	mv "$HARNESS.tmp" "$HARNESS"
fi
[[ $(git -C "$HARNESS" rev-parse HEAD) == "$BOLT_COMMIT" ]] || die "$HARNESS is not at $BOLT_COMMIT"

# The Pi-Bolt 0.8.5 release, checked against its pinned sha256.
ARCHIVE=$CACHE/pi-bolt-$PLATFORM-0.8.5.tar.xz
BOLT_DIR=$CACHE/pi-bolt-0.8.5-$PLATFORM
if [[ ! -x $BOLT_DIR/pi-bolt-$PLATFORM/pi ]]; then
	if [[ ! -f $ARCHIVE ]] || [[ $(sha256 "$ARCHIVE") != "$(bolt_sha256 "$PLATFORM")" ]]; then
		log "downloading the Pi-Bolt 0.8.5 release for $PLATFORM"
		curl -fsSL -o "$ARCHIVE.tmp" "$BOLT_URL/releases/download/$BOLT_TAG/pi-bolt-$PLATFORM.tar.xz"
		mv "$ARCHIVE.tmp" "$ARCHIVE"
	fi
	[[ $(sha256 "$ARCHIVE") == "$(bolt_sha256 "$PLATFORM")" ]] || die "$ARCHIVE does not match its pinned sha256"
	rm -rf "$BOLT_DIR"
	mkdir -p "$BOLT_DIR"
	tar -xJf "$ARCHIVE" -C "$BOLT_DIR"
fi
BOLT=$BOLT_DIR/pi-bolt-$PLATFORM/pi
[[ -x $BOLT ]] || die "no $BOLT in the release archive"

# pig: this tree's build unless --pig names one.
PIG_SHA=$(git -C "$ROOT" rev-parse --short=10 HEAD)
git -C "$ROOT" diff --quiet HEAD -- . ':!tmp' || PIG_SHA="$PIG_SHA-dirty"
if [[ -z $PIG ]]; then
	PIG=$OUT/bin/pig-bin
	log "building pig $PIG_SHA"
	(cd "$ROOT" && GOFLAGS=-p=2 "${LOCK[@]}" go build -p 2 -o "$PIG" ./cmd/pig)
else
	PIG_SHA="given:$PIG"
fi
[[ -x $PIG ]] || die "no pig at $PIG"

# Each launch gets a fresh HOME, so neither build reads the user's configuration or caches; the tools give each run its own
# agent directory (PI_CODING_AGENT_DIR, PIG_HOME). Inside the host bench slot (lane-run --bench sets PIG_BENCH_SLOT=1), benchcpu.py
# confine moves every process off the bench CPUs unless its environment carries PIG_BENCH_SLOT=1, and Pi-Bolt's harness strips
# PIG_* from the arms' environment, so each launcher restores the marker; without it a long row runs partly on the lane CPUs.
mark=''
[[ ${PIG_BENCH_SLOT:-} == 1 ]] && mark='export PIG_BENCH_SLOT=1'
for arm in pig bolt; do
	bin=$PIG
	[[ $arm == bolt ]] && bin=$BOLT
	cat >"$OUT/bin/$arm.sh" <<EOF
#!/bin/sh
$mark
export HOME="\$(mktemp -d "$OUT/homes/$arm.XXXXXX")"
exec "$bin" "\$@"
EOF
	chmod +x "$OUT/bin/$arm.sh"
done

{
	echo "date: $(date -u +%Y-%m-%dT%H:%MZ)"
	echo "host: $(uname -srm), $(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu) cpus, load $(load1)"
	echo "pig: $PIG_SHA ($("$PIG" --version 2>/dev/null | head -1))"
	echo "pi-bolt: $BOLT_TAG $BOLT_COMMIT, release sha256 $(bolt_sha256 "$PLATFORM") ($("$BOLT" --version 2>/dev/null | head -1))"
	echo "rounds: $ROUNDS, rows: $ROWS, quick: $QUICK"
} >>"$OUT/environment.txt"

if ((QUICK)); then
	LARGE=50 ANSWERS=5000 PROMPTS=10 EVERY=5
else
	LARGE=50,200 ANSWERS=20000,60000 PROMPTS=75 EVERY=25
fi
ARMS=(pig-a bolt-a pig-b bolt-b)
RUNS=$OUT/runs.jsonl

gate_load() {
	[[ -n ${LW_BENCH_MAX_LOAD:-} ]] || return 0
	until awk -v l="$(load1)" -v m="$LW_BENCH_MAX_LOAD" 'BEGIN{exit !(l < m)}'; do
		log "load $(load1) >= $LW_BENCH_MAX_LOAD; waiting"
		sleep 60
	done
}

run_row() {
	local row=$1 round=$2 builds=() b raw jsonl l0 status=0
	for b in "${ORDER[@]}"; do builds+=(--build "$b=$OUT/bin/${b%-*}.sh"); done
	raw=$OUT/raw/r$round-$row.txt
	jsonl=$OUT/raw/r$round-$row.jsonl
	rm -f "$jsonl"
	local cmd=(python3)
	case $row in
	large_write) cmd+=("$HARNESS/bench/large_write.py" --sizes "$LARGE") ;;
	long_answer) cmd+=("$HARNESS/bench/long_answer.py" --sizes "$ANSWERS") ;;
	interactive | headless | startup) cmd+=("$HARNESS/bench/benchmark.py" --scenarios "$row" --runs 1 --warmup 1 --out "$jsonl") ;;
	long_session) cmd+=("$HARNESS/bench/long_session.py" --prompts "$PROMPTS" --every "$EVERY" --out "$jsonl") ;;
	*) die "unknown row $row" ;;
	esac
	gate_load
	l0=$(load1)
	log "round $round/$ROUNDS $row (${ORDER[*]}), load $l0"
	(cd "$HARNESS" && "${LOCK[@]}" "${cmd[@]}" "${builds[@]}") >"$raw" 2>&1 || status=$?
	((status == 0)) || log "$row round $round exited $status (see $raw)"
	python3 "$ROOT/bench/lw/rows.py" collect --out "$RUNS" --row "$row" --round "$round" --raw "$raw" --jsonl "$jsonl" \
		--meta "wave=$WAVE" --meta "platform=$PLATFORM" --meta "pig=$PIG_SHA" --meta "bolt=$BOLT_TAG" --meta "load1_before=$l0" --meta "load1_after=$(load1)" \
		--meta "order=${ORDER[*]}" --meta "exit=$status" || log "$row round $round: nothing to collect"
}

IFS=, read -r -a ROW_LIST <<<"$ROWS"
for ((round = 1; round <= ROUNDS; round++)); do
	shift_by=$(((round - 1) % ${#ARMS[@]}))
	ORDER=("${ARMS[@]:shift_by}" "${ARMS[@]:0:shift_by}")
	for row in "${ROW_LIST[@]}"; do
		run_row "$row" "$round"
	done
	rm -rf "${OUT:?}/homes/"*
done

python3 "$ROOT/bench/lw/rows.py" summary "$RUNS" --ratchet "$ROOT/automation/perf/ratchet.toml" | tee "$OUT/summary.md"
if ((CHECK)); then
	python3 "$ROOT/bench/lw/rows.py" check "$RUNS" "$ROOT/automation/perf/ratchet.toml"
fi
