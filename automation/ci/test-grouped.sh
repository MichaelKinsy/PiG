#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

FAST_PARALLEL=${TEST_FAST_PARALLEL:-12}
# Subprocess-heavy packages launch real extension processes and bind Unix
# sockets. With per-test isolated socket dirs (see integration_test.go and
# conformance_test.go), package-level parallelism of 2 is safe. Raise via
# TEST_SUBPROCESS_PARALLEL only after proving with -count=3 first.
SUBPROCESS_PARALLEL=${TEST_SUBPROCESS_PARALLEL:-2}
SERIAL_PARALLEL=${TEST_SERIAL_PARALLEL:-1}
STRESS_COUNT=${TEST_STRESS_COUNT:-3}
# cmd/pig runs as CLI_SHARDS separate go test processes (test-shard-pattern.sh), each under go test's 10-minute package timeout. One process for the whole package needs more than 10 minutes on a 4-CPU runner. The Windows cli-1 to cli-8 jobs in .github/workflows/ci.yml run the same shards, one parallel job each; test/ci-images checks that the two agree.
CLI_SHARDS=8
CLI_PKG=./cmd/pig
MODE=${1:-default}
COUNT_FLAG=()
RUN_FLAG=()

HEAVY_PKGS=(
  "$CLI_PKG"
  ./coding/extension/host/subprocess
  ./test/extension-conformance
)
SERIAL_PKGS=(
)

all_pkgs=$(go list ./...)
mapfile -t ALL_PKGS <<<"$all_pkgs"
heavy_pkgs=$(go list "${HEAVY_PKGS[@]}")
mapfile -t HEAVY_PKGS <<<"$heavy_pkgs"
CLI_PKG=$(go list "$CLI_PKG")
if [[ ${#SERIAL_PKGS[@]} -gt 0 ]]; then
  serial_pkgs=$(go list "${SERIAL_PKGS[@]}")
  mapfile -t SERIAL_PKGS <<<"$serial_pkgs"
fi
exclude_pkg() {
  local pkg="$1"
  for heavy in "${HEAVY_PKGS[@]}"; do
    [[ "$pkg" == "$heavy" ]] && return 0
  done
  for serial in "${SERIAL_PKGS[@]}"; do
    [[ "$pkg" == "$serial" ]] && return 0
  done
  return 1
}

FAST_PKGS=()
for pkg in "${ALL_PKGS[@]}"; do
  if ! exclude_pkg "$pkg"; then
    FAST_PKGS+=("$pkg")
  fi
done

# Build once, then reuse everywhere. Agents should not rediscover build-cache
# contention between test packages; the scheduler handles it by default.
if [[ "$MODE" != "report" ]]; then
  fixture_exports=$("$ROOT/automation/ci/test-fixtures.sh" --exports)
  eval "$fixture_exports"
fi

# Tests that build Pig, a packed extension cell or a Piglet run their own `go build` in a child process. Those builds share no work with the `go test` action graph, so on a cold Go build cache (the first run after go.mod or go.sum changes) each one recompiles the whole dependency graph while the 12-way package pool competes for the same cores, and a build bounded by the 2-minute extension-validation deadline is killed. Compile each flag set once here so every child build links from the cache: -trimpath changes every package's action ID, and CGO_ENABLED changes every package that reaches net or os/user. The default set serves go test itself, the default-CGO -trimpath set serves `pig build` and native Piglet Binary builds (cmd/pig/build_command.go, coding/pigletbuild/native_build.go), and the CGO_ENABLED=0 -trimpath set serves extension and packed-cell builds (coding/extension/host/subprocess/builder.go, coding/extension/host/runtimecell/go_packed.go). An extension or packed-cell build compiles PiG as a dependency module, and -trimpath then maps its source directories to github.com/MichaelKinsy/PiG@<version>/... where the main-module builds above map them to github.com/MichaelKinsy/PiG/..., so those builds share no compiled PiG package with them (about 60 packages for the Porter extension). Building the in-repo Porter extension module with GOWORK=off (the packed build's setting) compiles that dependency-module set once.
if [[ "$MODE" != "report" ]]; then
  echo "[test-grouped] warming the Go build cache"
  go build ./...
  go build -trimpath ./...
  CGO_ENABLED=0 go build -trimpath ./...
  (cd piglets/porter/extensions/pig-porter && GOWORK=off CGO_ENABLED=0 go build -trimpath ./...)
fi

echo "[test-grouped] mode=$MODE"
echo "[test-grouped] fast-parallel ($FAST_PARALLEL): ${#FAST_PKGS[@]} packages"
echo "[test-grouped] subprocess-bounded ($SUBPROCESS_PARALLEL): ${#HEAVY_PKGS[@]} packages, $CLI_PKG in $CLI_SHARDS shards"
echo "[test-grouped] serial-exclusive ($SERIAL_PARALLEL): ${#SERIAL_PKGS[@]} packages"

SCRATCH=
trap '[[ -z "$SCRATCH" ]] || rm -rf "$SCRATCH"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

run_group() {
  local label="$1" parallel="$2"
  shift 2
  local pkgs=("$@")
  if [[ ${#pkgs[@]} -eq 0 ]]; then
    return 0
  fi
  echo "[test-grouped] >>> $label"
  # Run under a private temporary directory so a test that leaks scratch files or directories fails this group instead of filling the shared one, and remove it on every exit path.
  local status=0
  SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/pig-tests.XXXXXX")
  TMPDIR="$SCRATCH" go test -p "$parallel" "${COUNT_FLAG[@]}" "${RUN_FLAG[@]}" "${pkgs[@]}" || status=$?
  if [[ $status -eq 0 ]]; then
    "$ROOT/automation/ci/assert-clean-tmp.sh" "$SCRATCH" || status=$?
  fi
  rm -rf "$SCRATCH"
  SCRATCH=
  return "$status"
}

# run_cli_shards <parallel>: run every cmd/pig shard, continuing past a failing shard so one run reports all of them.
run_cli_shards() {
  local parallel="$1" shard status=0 pattern
  for ((shard = 1; shard <= CLI_SHARDS; shard++)); do
    pattern=$("$ROOT/automation/ci/test-shard-pattern.sh" "$shard" "$CLI_SHARDS" "$CLI_PKG")
    RUN_FLAG=(-run "$pattern")
    run_group "cli shard $shard of $CLI_SHARDS" "$parallel" "$CLI_PKG" || status=$?
    RUN_FLAG=()
  done
  return "$status"
}

# The other heavy packages run as one group; cmd/pig runs in its shards.
OTHER_HEAVY_PKGS=()
for pkg in "${HEAVY_PKGS[@]}"; do
  [[ "$pkg" == "$CLI_PKG" ]] || OTHER_HEAVY_PKGS+=("$pkg")
done

case "$MODE" in
  default)
    run_group fast-parallel "$FAST_PARALLEL" "${FAST_PKGS[@]}"
    run_group subprocess-bounded "$SUBPROCESS_PARALLEL" "${OTHER_HEAVY_PKGS[@]}"
    run_cli_shards "$SUBPROCESS_PARALLEL"
    run_group serial-exclusive "$SERIAL_PARALLEL" "${SERIAL_PKGS[@]}"
    ;;
  fast)
    run_group fast-parallel "$FAST_PARALLEL" "${FAST_PKGS[@]}"
    run_group serial-exclusive "$SERIAL_PARALLEL" "${SERIAL_PKGS[@]}"
    ;;
  cli)
    run_cli_shards "$SUBPROCESS_PARALLEL"
    ;;
  subprocess|conformance)
    case "$MODE" in
      subprocess) selected=./coding/extension/host/subprocess ;;
      conformance) selected=./test/extension-conformance ;;
    esac
    selected=$(go list "$selected")
    run_group "$MODE" "$SUBPROCESS_PARALLEL" "$selected"
    ;;
  stress)
    # Bypass the test cache and force N repeats so the scheduler is
    # actually exercised under contention. Higher parallelism on the
    # heavy bucket is also exercised here.
    echo "[test-grouped] stress: -count=$STRESS_COUNT"
    COUNT_FLAG=(-count="$STRESS_COUNT")
    run_group fast-parallel 8 "${FAST_PKGS[@]}"
    run_group subprocess-bounded 4 "${OTHER_HEAVY_PKGS[@]}"
    run_cli_shards 4
    run_group serial-exclusive 1 "${SERIAL_PKGS[@]}"
    ;;
  report)
    printf '[test-grouped] fast packages:\n'; printf '  %s\n' "${FAST_PKGS[@]}"
    printf '[test-grouped] subprocess packages:\n'; printf '  %s\n' "${OTHER_HEAVY_PKGS[@]}"
    printf '[test-grouped] sharded packages:\n  %s (%s shards)\n' "$CLI_PKG" "$CLI_SHARDS"
    printf '[test-grouped] serial packages:\n';
    if [[ ${#SERIAL_PKGS[@]} -eq 0 ]]; then echo '  (none)'; else printf '  %s\n' "${SERIAL_PKGS[@]}"; fi
    ;;
  *)
    echo "usage: $0 [default|fast|cli|subprocess|conformance|stress|report]" >&2
    exit 2
    ;;
esac
