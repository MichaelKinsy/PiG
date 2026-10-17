#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# One quiet-slot run of the eval suite: startup, overhead (20 runs, A/A), long-history, reliability, in that order, pinned to the core range CORES
# when it is set, starting no earlier than START_UTC. Results go to tmp/bench/<name>-<date>/ and are never overwritten. Exits non-zero if a bench failed.
# Usage: CORES=8-15 START_UTC=2026-10-11T02:00:00Z STAMP=2026-10-10 bench/evals/overnight.sh
set -u
cd "$(dirname "$0")"
CORES=${CORES:-}
PIN=()
[ -n "$CORES" ] && PIN=(taskset -c "$CORES")
failed=0
STAMP=${STAMP:-$(date -u +%F)}
OUT=${OUT:-$(cd ../.. && pwd)/tmp/bench}
mkdir -p "$OUT"
if [ -n "${START_UTC:-}" ]; then
  until [ "$(date -u +%s)" -ge "$(date -u -d "$START_UTC" +%s)" ]; do sleep 30; done
fi
run() { # name args...
  local name=$1; shift
  local dir="$OUT/$name-$STAMP"
  [ -e "$dir/raw.json" ] && { echo "skip $name: $dir/raw.json exists"; return; }
  echo "$(date -u +%FT%TZ) start $name"
  local rc=0
  "${PIN[@]}" python3 -m pigbench "$@" --out "$dir" > "$OUT/$name-$STAMP.log" 2>&1 < /dev/null || rc=$?
  echo "$(date -u +%FT%TZ) end $name exit $rc"  # rc, not $?: the date substitution would reset $? to 0
  [ "$rc" -eq 0 ] || failed=1
}
run startup startup --arms pig-0.5 pig-0.4.1 pi-1.1.0 rpi pibolt codex --rounds 8 --runs 5
run overhead overhead --arms pig-0.5 pig-0.4.1 pi-1.1.0 rpi pibolt codex --runs 20 --warmup 2 --aa pig-0.5 pi-1.1.0
run longhistory longhistory --runs 5
run reliability reliability --reps 5
exit "$failed"
