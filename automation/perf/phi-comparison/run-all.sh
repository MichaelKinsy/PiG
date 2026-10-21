#!/bin/sh
# SPDX-License-Identifier: MIT
# The whole matrix of items 1 and 2, one locked job per cell, results under $RESULTS (default ./results).
#   PIG_BIN PHI_BIN PIG_SCRATCH_BIN PHI_SCRATCH_BIN FLOOR_BIN must be set (see build.sh); N sets runs (default 30 first-frame), M rounds (default 7 memory).
#   run-all.sh [first-frame|memory|concurrent|extras|all]   (default all)
set -eu
here=$(cd "$(dirname "$0")" && pwd)
R="${RESULTS:-$PWD/results}"; mkdir -p "$R"
N="${N:-30}"; M="${M:-7}"
export PROBE_WORK="${PROBE_WORK:-/tmp/phi-cmp-work}"; mkdir -p "$PROBE_WORK"
part="${1:-all}"
want() { [ "$part" = all ] || [ "$part" = "$1" ]; }

if want first-frame; then
  # 1. first frame: cold / warm / warmhome, subjects interleaved round-robin
  for mode in cold warm warmhome; do
    "$here/run.sh" first-frame --subject floor,pig,phi,pig-trace --mode "$mode" --runs "$N" --out "$R/first-frame.jsonl"
  done
fi
if want extras; then
  # a terminal that never answers: how long each program waits for DA1 / OSC 11 (window long enough for phi's 500 ms and pig's wait)
  "$here/run.sh" first-frame --subject pig,phi --mode warm --silent --window 3 --runs 15 --out "$R/first-frame-silent.jsonl"
  # phi as a first install ships (update check on, fd/rg missing so it downloads both from github.com); informational, warm only
  "$here/run.sh" first-frame --subject phi,phi-fresh --mode warm --runs 10 --out "$R/first-frame-fresh.jsonl"
  "$here/run.sh" memory --subject phi-fresh --runs 3 --out "$R/memory-1-fresh.jsonl"
  # what returning start-up garbage to the OS (debug.FreeOSMemory) would give back, scratch builds
  for round in $(seq "$M"); do
    for s in pig-scratch phi-scratch; do "$here/run.sh" memory --subject "$s" --runs 1 --memclasses --freeos --out "$R/memory-1-freeos.jsonl"; done
  done
fi
if want memory; then
  # 2. memory, one session: rounds interleave the subjects; scratch builds also dump Go memory classes
  for round in $(seq "$M"); do
    for s in pig phi; do "$here/run.sh" memory --subject "$s" --runs 1 --out "$R/memory-1.jsonl"; done
    for s in pig-scratch pig-scratch-nohljs phi-scratch; do "$here/run.sh" memory --subject "$s" --runs 1 --memclasses --out "$R/memory-1-scratch.jsonl"; done
    "$here/run.sh" memory --subject pig-metrics --runs 1 --clean-exit --out "$R/memory-1-metrics.jsonl"
  done
fi
if want concurrent; then
  # 3. ten concurrent sessions
  for round in $(seq 5); do
    for s in pig phi; do "$here/run.sh" memory --subject "$s" --sessions 10 --runs 1 --out "$R/memory-10.jsonl"; done
  done
fi
