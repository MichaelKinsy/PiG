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
CLI_PKG=./coding/cli
SUBPROCESS_PKG=./coding/extension/host/subprocess
CONFORMANCE_PKG=./test/extension-conformance
# Heavy packages that run as several go test processes, each under its own package timeout and each warm from the shared Go and Cargo caches, like cmd/pig: <package>:<shards>. test-shard-pattern.sh partitions each package's tests by name hash, and run_sharded_pkg refuses to run a package unless its shard patterns select every listed test exactly once. changed mode runs these packages whole.
SHARDED_SPEC=(
  ./ai:7
  ./coding:4
  ./internal/codingagent:8
  ./test/parity/interface-closure/autobind:4
  ./chord/host:2
  ./codemode:2
  ./durable/codemode:2
  ./coding/extension/host/subprocess:3
  ./test/extension-conformance:2
)
MODE=${1:-default}
COUNT_FLAG=()
# CI restores the Go build cache, test results included, from earlier commits, and the scratch paths below are stable, so a result could replay there. The cache key misses inputs a child process reads (a script, a Node or Python oracle, a fixture build), so CI runs every test.
if [[ -n "${CI:-}" ]]; then
  COUNT_FLAG=(-count=1)
fi
RUN_FLAG=()
RACE_FLAG=()
# changed mode streams go test -json into test-json-report.py and keeps each group's raw events under JSON_DIR.
JSON_DIR=
JSON_LABEL=
TAG_FLAG=()

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
SUBPROCESS_PKG=$(go list "$SUBPROCESS_PKG")
CONFORMANCE_PKG=$(go list "$CONFORMANCE_PKG")
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

# changed mode: test only the packages the change can affect (automation/ci/changed-packages.py), under the race detector for the packages the change edits directly.
DIRECT_PKGS=()
if [[ "$MODE" == changed ]]; then
  selection=$(python3 "$ROOT/automation/ci/changed-packages.py" ${TEST_BASE:+--base "$TEST_BASE"})
  AFFECTED=()
  while read -r kind entry; do
    case "$kind" in
      direct) DIRECT_PKGS+=("$entry") ;;
      affected) AFFECTED+=("$entry") ;;
      tagged) echo "[test-grouped] changed: $entry builds only under a tag; run its own target (make parity-family, make test-integration)" ;;
      unmapped) echo "[test-grouped] changed: no package test reads $entry; CI's full suite covers it" ;;
    esac
  done <<<"$selection"
  in_list() {
    local needle="$1" item
    shift
    for item in "$@"; do [[ "$item" == "$needle" ]] && return 0; done
    return 1
  }
  case "${TEST_CHANGED_RACE:-direct}" in
    direct) ;;
    affected) DIRECT_PKGS=("${AFFECTED[@]}") ;;
    none) DIRECT_PKGS=() ;;
    *) echo "TEST_CHANGED_RACE must be direct, affected or none" >&2; exit 2 ;;
  esac
  kept=()
  for pkg in "${FAST_PKGS[@]}"; do in_list "$pkg" "${AFFECTED[@]}" && kept+=("$pkg"); done
  FAST_PKGS=("${kept[@]}")
  kept=()
  for pkg in "${HEAVY_PKGS[@]}"; do in_list "$pkg" "${AFFECTED[@]}" && kept+=("$pkg"); done
  HEAVY_PKGS=("${kept[@]}")
  SERIAL_PKGS=()
  JSON_DIR="$ROOT/tmp/test-changed/$(date -u +%Y%m%dT%H%M%SZ)"
  mkdir -p "$JSON_DIR"
fi

# Resolve the sharded packages that exist in this module and take them out of the whole-package groups.
declare -A SHARD_COUNT=()
if [[ "$MODE" != changed && "$MODE" != report ]]; then
  for spec in "${SHARDED_SPEC[@]}"; do
    spec_pkg=$(go list "${spec%:*}")
    in_all=
    for pkg in "${ALL_PKGS[@]}"; do [[ "$pkg" == "$spec_pkg" ]] && in_all=1; done
    [[ -n "$in_all" ]] && SHARD_COUNT[$spec_pkg]=${spec##*:}
  done
  kept=()
  for pkg in "${FAST_PKGS[@]}"; do [[ -n ${SHARD_COUNT[$pkg]:-} ]] || kept+=("$pkg"); done
  FAST_PKGS=("${kept[@]}")
  kept=()
  for pkg in "${HEAVY_PKGS[@]}"; do [[ -n ${SHARD_COUNT[$pkg]:-} ]] || kept+=("$pkg"); done
  HEAVY_PKGS_WHOLE=("${kept[@]}")
else
  HEAVY_PKGS_WHOLE=("${HEAVY_PKGS[@]}")
fi

# Build once, then reuse everywhere. Agents should not rediscover build-cache
# contention between test packages; the scheduler handles it by default.
if [[ "$MODE" != "report" ]]; then
  fixture_exports=$("$ROOT/automation/ci/test-fixtures.sh" --exports)
  eval "$fixture_exports"
fi

# Warm with go list -export: it compiles every package into the build cache and links nothing, where go build ./... links every command and discards it on each run.
# Tests that build Pig, a packed extension cell or a Piglet run their own `go build` in a child process. Those builds share no work with the `go test` action graph, so on a cold Go build cache (the first run after go.mod or go.sum changes) each one recompiles the whole dependency graph while the 12-way package pool competes for the same cores, and a build bounded by the 2-minute extension-validation deadline is killed. Compile each flag set once here so every child build links from the cache: -trimpath changes every package's action ID, and CGO_ENABLED changes every package that reaches net or os/user. The default set serves go test itself, the default-CGO -trimpath set serves `pig build` and native Piglet Binary builds (coding/cli/build_command.go, coding/pigletbuild/native_build.go), and the CGO_ENABLED=0 -trimpath set serves extension and packed-cell builds (coding/extension/host/subprocess/builder.go, coding/extension/host/runtimecell/go_packed.go). An extension or packed-cell build compiles PiG as a dependency module, and -trimpath then maps its source directories to github.com/MichaelKinsy/PiG@<version>/... where the main-module builds above map them to github.com/MichaelKinsy/PiG/..., so those builds share no compiled PiG package with them (about 60 packages for the Porter extension). Building the in-repo Porter extension module with GOWORK=off (the packed build's setting) compiles that dependency-module set once.
# Only the heavy packages run those child builds, so changed mode warms the cache only when it tests one of them.
if [[ "$MODE" != "report" && ( "$MODE" != changed || ${#HEAVY_PKGS[@]} -gt 0 ) ]]; then
  echo "[test-grouped] warming the Go build cache"
  go list -export -f '' ./... >/dev/null
  go list -trimpath -export -f '' ./... >/dev/null
  CGO_ENABLED=0 go list -trimpath -export -f '' ./... >/dev/null
  (cd piglets/porter/extensions/pig-porter && GOWORK=off CGO_ENABLED=0 go list -trimpath -export -f '' ./... >/dev/null)
fi

echo "[test-grouped] mode=$MODE"
echo "[test-grouped] fast-parallel ($FAST_PARALLEL): ${#FAST_PKGS[@]} packages"
echo "[test-grouped] subprocess-bounded ($SUBPROCESS_PARALLEL): ${#HEAVY_PKGS[@]} packages, $CLI_PKG in $CLI_SHARDS shards"
echo "[test-grouped] serial-exclusive ($SERIAL_PARALLEL): ${#SERIAL_PKGS[@]} packages"

SCRATCH=
AGENT_GUARD=
# Every group of every run in this checkout uses the same scratch and agent-directory paths. Tests read TMPDIR and the agent-directory variables, and the go test cache keys a result on the values a test read, so a fresh random path per run made every result uncacheable: rerunning make test-changed with nothing changed took 3,764 CPU-seconds. The paths are emptied before each group and removed after it, and a lock keeps two runs in one checkout from sharing them.
STABLE_KEY=$(printf '%s' "$ROOT" | cksum | cut -d' ' -f1)
STABLE_BASE="${TMPDIR:-/tmp}"
STABLE_BASE=${STABLE_BASE%/}
if [[ "$MODE" != "report" ]]; then
  mkdir -p "$ROOT/tmp"
  exec {RUN_LOCK}>"$ROOT/tmp/test-grouped.lock"
  if command -v flock >/dev/null 2>&1 && ! flock -n "$RUN_LOCK"; then
    echo "[test-grouped] another test-grouped.sh run is using this checkout; waiting for it" >&2
    flock "$RUN_LOCK"
  fi
fi
trap '[[ -z "$SCRATCH" ]] || rm -rf "$SCRATCH"; [[ -z "$AGENT_GUARD" ]] || rm -rf "$AGENT_GUARD" "$AGENT_GUARD.manifest"' EXIT
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
  # The paths are predictable, so each must be a fresh directory this run created: a path another user holds fails the group instead of receiving its files. run_group runs under || in changed mode, where set -e does not apply.
  SCRATCH="$STABLE_BASE/pig-tests.$STABLE_KEY"
  # Run under seeded agent directories. A lane or a developer exports PIG_CODING_AGENT_DIR for the pig that is running the tests, and a test that does not replace it writes that real directory. The guard fails the group when any seeded file changes or appears, whether or not the tests passed.
  AGENT_GUARD="$STABLE_BASE/pig-agent-guard.$STABLE_KEY"
  # umask 077 makes mkdir create each directory private without the separate chmod that mkdir -m runs afterwards, which Git Bash on Windows refuses on a directory in the temporary directory ("cannot change permissions: Permission denied").
  if ! { rm -rf "$SCRATCH" "$AGENT_GUARD" "$AGENT_GUARD.manifest" && (umask 077 && mkdir "$SCRATCH" "$AGENT_GUARD"); }; then
    echo "[test-grouped] cannot create the scratch directories $SCRATCH and $AGENT_GUARD for $label" >&2
    SCRATCH=
    AGENT_GUARD=
    return 1
  fi
  # A failed seed leaves the caller's agent directories exported, so the group fails without running go test.
  local exports
  if exports=$("$ROOT/automation/ci/agent-dir-guard.sh" seed "$AGENT_GUARD"); then
    eval "$exports"
  else
    status=$?
    echo "[test-grouped] could not seed isolated agent directories for $label" >&2
    rm -rf "$SCRATCH" "$AGENT_GUARD" "$AGENT_GUARD.manifest"
    SCRATCH=
    AGENT_GUARD=
    return "$status"
  fi
  if [[ -n "$JSON_DIR" ]]; then
    JSON_LABEL=$((JSON_LABEL + 1))
    # The tests get no copy of the checkout lock: a process they leak would otherwise hold it and block every later run here.
    TMPDIR="$SCRATCH" go test -json -p "$parallel" "${TAG_FLAG[@]}" "${COUNT_FLAG[@]}" "${RACE_FLAG[@]}" "${RUN_FLAG[@]}" "${pkgs[@]}" {RUN_LOCK}>&- | python3 "$ROOT/automation/ci/test-json-report.py" --out "$JSON_DIR/$JSON_LABEL-${label//[^A-Za-z0-9]/-}.json" || status=$?
  else
    TMPDIR="$SCRATCH" go test -p "$parallel" "${TAG_FLAG[@]}" "${COUNT_FLAG[@]}" "${RACE_FLAG[@]}" "${RUN_FLAG[@]}" "${pkgs[@]}" {RUN_LOCK}>&- || status=$?
  fi
  "$ROOT/automation/ci/reap-test-processes.sh" "$SCRATCH" || status=$?
  "$ROOT/automation/ci/agent-dir-guard.sh" check "$AGENT_GUARD" || status=$?
  if [[ $status -eq 0 ]]; then
    "$ROOT/automation/ci/assert-clean-tmp.sh" "$SCRATCH" || status=$?
  fi
  rm -rf "$SCRATCH" "$AGENT_GUARD" "$AGENT_GUARD.manifest"
  SCRATCH=
  AGENT_GUARD=
  return "$status"
}

# run_cli_shards <parallel> [<first> <last>]: run the cmd/pig shards first..last (default every shard), continuing past a failing shard so one run reports all of them.
run_cli_shards() {
  local parallel="$1" first="${2:-1}" last="${3:-$CLI_SHARDS}" shard status=0 pattern
  if ! [[ "$first" =~ ^[0-9]+$ && "$last" =~ ^[0-9]+$ ]] || (( first < 1 || last > CLI_SHARDS || first > last )); then
    echo "[test-grouped] cli: shard range $first..$last is not within 1..$CLI_SHARDS" >&2
    return 2
  fi
  for ((shard = first; shard <= last; shard++)); do
    pattern=$("$ROOT/automation/ci/test-shard-pattern.sh" "$shard" "$CLI_SHARDS" "$CLI_PKG")
    RUN_FLAG=(-run "$pattern")
    run_group "cli shard $shard of $CLI_SHARDS" "$parallel" "$CLI_PKG" || status=$?
    RUN_FLAG=()
  done
  return "$status"
}

# run_sharded_pkg <label> <parallel> <package> <shards>: run each shard of the package as its own go test process, continuing past a failing shard, after proving the shard patterns select every listed test exactly once.
run_sharded_pkg() {
  local label="$1" parallel="$2" pkg="$3" shards="$4" shard status=0 pattern
  "$ROOT/automation/ci/check-shard-union.sh" "$pkg" "$shards" || return $?
  for ((shard = 1; shard <= shards; shard++)); do
    pattern=$("$ROOT/automation/ci/test-shard-pattern.sh" "$shard" "$shards" "$pkg")
    RUN_FLAG=(-run "$pattern")
    run_group "$label $pkg shard $shard of $shards" "$parallel" "$pkg" || status=$?
    RUN_FLAG=()
  done
  return "$status"
}

# run_sharded <parallel> [<package>...]: run the sharded packages, or only the named ones.
run_sharded() {
  local parallel="$1" status=0 pkg
  shift
  local only=("$@")
  for pkg in "${!SHARD_COUNT[@]}"; do
    if [[ ${#only[@]} -gt 0 ]]; then
      local hit=
      for o in "${only[@]}"; do [[ "$o" == "$pkg" ]] && hit=1; done
      [[ -n "$hit" ]] || continue
    fi
    run_sharded_pkg sharded "$parallel" "$pkg" "${SHARD_COUNT[$pkg]}" || status=$?
  done
  return "$status"
}

# run_fault_group: the Pi tests that need ControlledStorage run in the pigfault build (durable/core/driver/fault_on.go): the owner goroutine is held
# between a step's commits. The production build has no such entry point; the normal run of the package skips these files. The filter names exactly the tests the tagged files declare, so every one runs and the package's untagged tests, which the normal run covers, do not run twice.
run_fault_group() {
  local tests
  tests=$(grep -l '^//go:build pigfault' "$ROOT"/durable/core/driver/*_test.go | xargs sed -n 's/^func \(Test[A-Za-z0-9_]*\)(t \*testing\.T).*/\1/p' | paste -sd'|' -)
  if [[ -z "$tests" ]]; then
    echo "[test-grouped] core-fault: no test in the pigfault-tagged files of ./durable/core/driver" >&2
    return 1
  fi
  TAG_FLAG=(-tags pigfault)
  RUN_FLAG=(-run "^($tests)\$")
  run_group core-fault "$1" ./durable/core/driver || return $?
  TAG_FLAG=()
  RUN_FLAG=()
}

# The other heavy packages run as one group; cmd/pig runs in its shards.
OTHER_HEAVY_PKGS=()
for pkg in "${HEAVY_PKGS_WHOLE[@]}"; do
  [[ "$pkg" == "$CLI_PKG" ]] || OTHER_HEAVY_PKGS+=("$pkg")
done

case "$MODE" in
  default)
    run_group fast-parallel "$FAST_PARALLEL" "${FAST_PKGS[@]}"
    run_sharded "$SUBPROCESS_PARALLEL"
    run_fault_group "$SERIAL_PARALLEL"
    run_group subprocess-bounded "$SUBPROCESS_PARALLEL" "${OTHER_HEAVY_PKGS[@]}"
    run_cli_shards "$SUBPROCESS_PARALLEL"
    run_group serial-exclusive "$SERIAL_PARALLEL" "${SERIAL_PKGS[@]}"
    ;;
  fast|fast-parallel|fast-sharded)
    # fast is fast-parallel and fast-sharded in one run. CI runs the two halves on separate jobs, because together they
    # were the run's longest job.
    if [[ "$MODE" != fast-sharded ]]; then
      run_group fast-parallel "$FAST_PARALLEL" "${FAST_PKGS[@]}"
    fi
    if [[ "$MODE" != fast-parallel ]]; then
      # Every sharded package but the extension host and the conformance suite, which test-subprocess and test-conformance run.
      fast_sharded=()
      for pkg in "${!SHARD_COUNT[@]}"; do [[ "$pkg" == "$SUBPROCESS_PKG" || "$pkg" == "$CONFORMANCE_PKG" ]] || fast_sharded+=("$pkg"); done
      [[ ${#fast_sharded[@]} -eq 0 ]] || run_sharded "$SUBPROCESS_PARALLEL" "${fast_sharded[@]}"
    fi
    if [[ "$MODE" != fast-sharded ]]; then
      run_fault_group "$SERIAL_PARALLEL"
      run_group serial-exclusive "$SERIAL_PARALLEL" "${SERIAL_PKGS[@]}"
    fi
    ;;
  cli)
    # cli [<first> <last>]: CI runs the shards in two jobs; make test-cli runs all of them.
    run_cli_shards "$SUBPROCESS_PARALLEL" "${2:-1}" "${3:-$CLI_SHARDS}"
    ;;
  subprocess|conformance)
    case "$MODE" in
      subprocess) selected=./coding/extension/host/subprocess ;;
      conformance) selected=./test/extension-conformance ;;
    esac
    selected=$(go list "$selected")
    if [[ -n ${SHARD_COUNT[$selected]:-} ]]; then
      run_sharded "$SUBPROCESS_PARALLEL" "$selected"
    else
      run_group "$MODE" "$SUBPROCESS_PARALLEL" "$selected"
    fi
    ;;
  stress)
    # Bypass the test cache and force N repeats so the scheduler is
    # actually exercised under contention. Higher parallelism on the
    # heavy bucket is also exercised here.
    echo "[test-grouped] stress: -count=$STRESS_COUNT"
    COUNT_FLAG=(-count="$STRESS_COUNT")
    run_group fast-parallel 8 "${FAST_PKGS[@]}"
    run_fault_group 1
    run_sharded 4
    run_group subprocess-bounded 4 "${OTHER_HEAVY_PKGS[@]}"
    run_cli_shards 4
    run_group serial-exclusive 1 "${SERIAL_PKGS[@]}"
    ;;
  changed)
    echo "[test-grouped] changed: ${#DIRECT_PKGS[@]} edited packages run under -race (TEST_CHANGED_RACE=${TEST_CHANGED_RACE:-direct}); events in $JSON_DIR"
    status=0
    race_fast=() plain_fast=() race_heavy=() plain_heavy=()
    for pkg in "${FAST_PKGS[@]}"; do
      if in_list "$pkg" "${DIRECT_PKGS[@]}"; then race_fast+=("$pkg"); else plain_fast+=("$pkg"); fi
    done
    for pkg in "${OTHER_HEAVY_PKGS[@]}"; do
      if in_list "$pkg" "${DIRECT_PKGS[@]}"; then race_heavy+=("$pkg"); else plain_heavy+=("$pkg"); fi
    done
    RACE_FLAG=(-race)
    run_group "changed fast-parallel race" "$FAST_PARALLEL" "${race_fast[@]}" || status=$?
    run_group "changed subprocess-bounded race" "$SUBPROCESS_PARALLEL" "${race_heavy[@]}" || status=$?
    RACE_FLAG=()
    run_group "changed fast-parallel" "$FAST_PARALLEL" "${plain_fast[@]}" || status=$?
    run_group "changed subprocess-bounded" "$SUBPROCESS_PARALLEL" "${plain_heavy[@]}" || status=$?
    if in_list "$CLI_PKG" "${HEAVY_PKGS[@]}"; then
      in_list "$CLI_PKG" "${DIRECT_PKGS[@]}" && RACE_FLAG=(-race)
      run_cli_shards "$SUBPROCESS_PARALLEL" || status=$?
      RACE_FLAG=()
    fi
    if [[ ${#FAST_PKGS[@]} -eq 0 && ${#HEAVY_PKGS[@]} -eq 0 ]]; then echo "[test-grouped] changed: no Go package is affected"; fi
    exit "$status"
    ;;
  report)
    printf '[test-grouped] fast packages:\n'; printf '  %s\n' "${FAST_PKGS[@]}"
    printf '[test-grouped] subprocess packages:\n'; printf '  %s\n' "${OTHER_HEAVY_PKGS[@]}"
    printf '[test-grouped] sharded packages:\n  %s (%s shards)\n' "$CLI_PKG" "$CLI_SHARDS"
    printf '  %s\n' "${SHARDED_SPEC[@]}"
    printf '[test-grouped] serial packages:\n';
    if [[ ${#SERIAL_PKGS[@]} -eq 0 ]]; then echo '  (none)'; else printf '  %s\n' "${SERIAL_PKGS[@]}"; fi
    ;;
  *)
    echo "usage: $0 [default|fast|fast-parallel|fast-sharded|cli [<first> <last>]|subprocess|conformance|changed|stress|report]" >&2
    exit 2
    ;;
esac
