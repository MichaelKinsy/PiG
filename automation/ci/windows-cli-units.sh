#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -uo pipefail

# Runs the cmd/pig shards (cli-1 to cli-8 of make test-cli) that one Windows job holds.
#   UNITS='cli-2 cli-5' windows-cli-units.sh
# go test hangs on GitHub's Windows runners before the test binary starts (golang/go#81368: go.exe spins, no test
# process runs), so the job builds the test binary once with go test -c and lists and runs it directly from the package
# directory, as go test would. -test.v streams every test's start and end; each shard runs in its own process under the
# 25-minute alarm, and every shard runs even when an earlier one failed.

read -r -a units <<< "${UNITS:?UNITS lists the cmd/pig shards of the job}"
package=coding/cli
bin="$RUNNER_TEMP/cli-test.exe"
# Timestamps split module download and compile, the listing (which runs the test binary's TestMain), and each run.
echo "$(date -u +%T) building the ./$package test binary"
if ! timeout 10m go test -c -o "$bin" ./$package; then
  echo "::error::building the ./$package test binary failed"
  exit 1
fi
status=0
for unit in "${units[@]}"; do
  shard=${unit#cli-}
  # Each shard gets its own home with a space in its path, as each shard's job had.
  acceptance="$RUNNER_TEMP/pig native acceptance/$unit"
  mkdir -p "$acceptance"
  export PIG_HOME="$acceptance/config with spaces"
  export PIG_CODING_AGENT_DIR="$PIG_HOME/agent"
  echo "::group::$unit"
  failed=0
  echo "$(date -u +%T) listing ./$package shard $shard"
  if run=$(TEST_SHARD_BINARY="$bin" timeout 5m automation/ci/test-shard-pattern.sh "$shard" 8 ./$package); then
    echo "$(date -u +%T) running shard $shard"
    if [[ -n ${PIG_TEST_JSON_DIR:-} ]]; then
      # Keep the run's events for the shard-weight timings: the binary's -test.v=test2json output through test2json, run straight from the toolchain because go test is what hangs. Under pipefail a failing test binary fails the pipeline, whatever test2json and the report exit with. The report prints the same text as -test.v.
      mkdir -p "$PIG_TEST_JSON_DIR"
      py=$(command -v python3 || command -v python)
      import_path=$(go list "./$package")
      # The toolchain does not ship test2json prebuilt: go tool -n builds it into the build cache if needed and prints the binary's path (test2json.exe on Windows). Resolving it here keeps go.exe out of the pipeline below.
      test2json=$(go tool -n test2json) || { echo "::error::cannot build test2json"; failed=1; }
      (cd "$package" && "$bin" -test.paniconexit0 -test.v=test2json -test.timeout 25m -test.run "$run") | "$test2json" -p "$import_path" | "$py" automation/ci/test-json-report.py --verbatim --out "$PIG_TEST_JSON_DIR/$(date -u +%Y%m%dT%H%M%S)-$unit.json" || failed=1
    else
      (cd "$package" && "$bin" -test.paniconexit0 -test.v -test.timeout 25m -test.run "$run") || failed=1
    fi
  else
    failed=1
  fi
  echo "::endgroup::"
  if [ "$failed" -ne 0 ]; then
    echo "::error::cmd/pig shard $shard failed"
    status=1
  fi
done
exit "$status"
