#!/bin/bash
# SPDX-License-Identifier: MIT
# Reproduces the provider-frame allocation comparison (PiG anthropic-text vs phi) on xd670.
#   PIG_SRC=~/phi-cmp/pig PHI_SRC=~/phi-cmp/phi OUT=~/phi-cmp/framealloc-out ./run-frame.sh [build|bench|prof|pprof|all]
# PIG_SRC and PHI_SRC are never modified; phi is copied to $SCRATCH_PHI and the .txt benchmark is added there.
set -euo pipefail
PIG_SRC=${PIG_SRC:-$HOME/phi-cmp/pig}
PHI_SRC=${PHI_SRC:-$HOME/phi-cmp/phi}
OUT=${OUT:-$HOME/phi-cmp/framealloc-out}
BIN=${BIN:-$HOME/phi-cmp/framealloc-bin}
SCRATCH_PHI=${SCRATCH_PHI:-$HOME/phi-cmp/framealloc-phi}
HERE=$(cd "$(dirname "$0")" && pwd)
export PATH=$HOME/.local/go/bin:$PATH GOCACHE=$HOME/phi-cmp/gocache GOMODCACHE=$HOME/phi-cmp/gomod GOTOOLCHAIN=go1.27.3
mkdir -p "$OUT" "$BIN"
MEAS="flock /tmp/perf-xd670.lock taskset -c 0-63"
PIGB='ProviderStreamFrames/anthropic-text$'
PIGT="$BIN/pig-ai.test"; PHIT="$BIN/phi-anthropic.test"

build() {
  [ -d "$SCRATCH_PHI" ] || cp -a "$PHI_SRC" "$SCRATCH_PHI"
  cp "$HERE/phi_stream_frames_bench_test.go.txt" "$SCRATCH_PHI/internal/llm/anthropic/phi_stream_frames_bench_test.go"
  (cd "$PIG_SRC" && taskset -c 64-95,160-191 nice -n 10 go test -c -o "$PIGT" ./ai)
  (cd "$SCRATCH_PHI" && taskset -c 64-95,160-191 nice -n 10 go test -c -o "$PHIT" ./internal/llm/anthropic)
}

bench() { # N=10 runs, 20 iterations each, wall-clock + benchmem
  (cd "$PIG_SRC/ai" && $MEAS "$PIGT" -test.run '^$' -test.bench "$PIGB" -test.benchmem -test.count 10 -test.benchtime 20x) > "$OUT/pig-bench.txt" 2>&1
  (cd "$SCRATCH_PHI/internal/llm/anthropic" && $MEAS "$PHIT" -test.run '^$' -test.bench 'BenchmarkFrame(Decode|E2E)/' -test.benchmem -test.count 10 -test.benchtime 20x) > "$OUT/phi-bench.txt" 2>&1
}

prof() { # deterministic allocation profiles: every allocation sampled, one P
  (cd "$PIG_SRC/ai" && GOMAXPROCS=1 $MEAS "$PIGT" -test.run '^$' -test.bench "$PIGB" -test.benchtime=20x -test.memprofile "$OUT/pig-anthropic-text.mem" -test.memprofilerate=1) > "$OUT/pig-prof.txt" 2>&1
  for v in Decode/count Decode/agent E2E/count E2E/agent; do
    n=$(echo "$v" | tr / -)
    (cd "$SCRATCH_PHI/internal/llm/anthropic" && GOMAXPROCS=1 $MEAS "$PHIT" -test.run '^$' -test.bench "BenchmarkFrame$v\$" -test.benchtime=20x -test.memprofile "$OUT/phi-$n.mem" -test.memprofilerate=1) > "$OUT/phi-prof-$n.txt" 2>&1
  done
}

pp() { # pp <label> <binary> <mem> <args...>
  local label=$1 bin=$2 mem=$3; shift 3
  go tool pprof -nodecount=40 "$@" "$bin" "$mem" > "$OUT/pprof-$label.txt" 2>&1 || true
}

pprof() {
  for si in alloc_objects alloc_space; do
    for k in flat cum; do
      pp "pig-$si-$k" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -$k -sample_index=$si
    done
    # denominators and focus variants (-top header prints "Showing nodes accounting for X of Y total" after focus)
    pp "pig-$si-total" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -sample_index=$si -nodecount=1
    # as written in the assignment: matches the root frame continuationTurn.run.func1 of every stack, so it is ~all allocations
    pp "pig-$si-focus-continuation-suspend" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -cum -sample_index=$si -nodecount=5 -focus='continuation|suspend'
    pp "pig-$si-focus-continuation-suspend-Promise" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -cum -sample_index=$si -nodecount=5 -focus='continuation|suspend|Promise'
    pp "pig-$si-focus-jsmicrotask" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -cum -sample_index=$si -nodecount=5 -focus='jsmicrotask'   # symbol does not exist: empty
    # the suspension primitives themselves (every reader.suspend() ends in suspendContinuation -> awaitResolved)
    pp "pig-$si-focus-suspend-primitives" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -cum -sample_index=$si -nodecount=10 -focus='suspendContinuation|deferContinuation|awaitResolved|deferResolved|settleDeferred'
    # the emulation data types and the consumer-side continuation iterator (root frame continuationTurn excluded by name)
    pp "pig-$si-focus-emulation-types" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -cum -sample_index=$si -nodecount=10 -focus='continuation(EventIterator|Promise|Queue|Job|Executor|Then|Reactor|Watcher)|awaitResolved|suspendContinuation|deferContinuation|deferResolved|settleDeferred'
    pp "pig-$si-lines-flat" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -flat -lines -sample_index=$si -nodecount=30
    # provider decode path: everything under the provider's streamResponse body (excludes httptest server, client dial/headers, consumer loop)
    pp "pig-$si-under-streamResponse" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -cum -sample_index=$si -nodecount=40 -focus='anthropicProvider..streamResponse'
    pp "pig-$si-under-observeEvents" "$PIGT" "$OUT/pig-anthropic-text.mem" -top -cum -sample_index=$si -nodecount=40 -focus='observeEvents|RefreshEvent'
    for v in Decode-count Decode-agent E2E-count E2E-agent; do
      pp "phi-$v-$si-flat" "$PHIT" "$OUT/phi-$v.mem" -top -flat -sample_index=$si
      pp "phi-$v-$si-cum" "$PHIT" "$OUT/phi-$v.mem" -top -cum -sample_index=$si
      pp "phi-$v-$si-total" "$PHIT" "$OUT/phi-$v.mem" -top -sample_index=$si -nodecount=1
    done
  done
}

gate() { # the repo's own per-frame gates (marginal allocs and bytes between two stream lengths)
  (cd "$PIG_SRC/ai" && $MEAS "$PIGT" -test.run 'TestStreamFrameAllocationCeilings|TestProviderStreamPerFrameAllocationGate/anthropic-text' -test.v -test.count 3) > "$OUT/pig-gate.txt" 2>&1
}

case "${1:-all}" in
  gate) gate;;
  build) build;; bench) bench;; prof) prof;; pprof) pprof;;
  all) build; bench; gate; prof; pprof;;
esac
