#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -uo pipefail

# Runs the macOS test units of one kind that a grouped macOS job holds, each in its own go test process and timeout, as
# separate jobs did. Every unit runs even when an earlier one fails; the script fails if any unit failed.
#   UNITS='cli-2 cli-8 extension-host-2 packages' macos-units.sh cli|extension-host|packages
# Units: cli-1 to cli-8 (the cmd/pig shards of make test-cli), extension-host-1 to extension-host-3, packages.
# Each go test streams its events through test-json-report.py, as the changed-packages run of test-grouped.sh does: a line for
# each package, the output of every failed test, and the unit's ten slowest tests. Those times are what the jobs are packed by
# (ci-tiers.txt), and they name the test to look at when a unit nears its job's limit. The report exits 0; pipefail keeps go test's status.

kind=${1:?usage: UNITS=... macos-units.sh cli|extension-host|packages}
read -r -a units <<< "${UNITS:?UNITS lists the units of the job}"
status=0
ran=0
for unit in "${units[@]}"; do
  case "$kind:$unit" in
    cli:cli-*|extension-host:extension-host-*|packages:packages) ;;
    *) continue ;;
  esac
  ran=$((ran + 1))
  # Each unit gets its own home with a space in its path, as each unit's job had.
  acceptance="$RUNNER_TEMP/pig native acceptance/$unit"
  mkdir -p "$acceptance"
  export PIG_HOME="$acceptance/config with spaces"
  export PIG_CODING_AGENT_DIR="$PIG_HOME/agent"
  echo "::group::$unit"
  failed=0
  case "$unit" in
    cli-*)
      if run=$(automation/ci/test-shard-pattern.sh "${unit#cli-}" 8 ./coding/cli); then
        go test -timeout 30m -json -run "$run" ./coding/cli | python3 automation/ci/test-json-report.py || failed=1
      else
        failed=1
      fi
      ;;
    extension-host-*)
      if run=$(automation/ci/test-shard-pattern.sh "${unit##*-}" 3 ./coding/extension/host/subprocess); then
        go test -timeout 30m -json -run "$run" ./coding/extension/host/subprocess | python3 automation/ci/test-json-report.py || failed=1
      else
        failed=1
      fi
      ;;
    packages)
      go test -timeout 30m -json ./env ./coding/piglet | python3 automation/ci/test-json-report.py || failed=1
      ;;
  esac
  echo "::endgroup::"
  if [ "$failed" -ne 0 ]; then
    echo "::error::macOS unit $unit failed"
    status=1
  fi
done
if [ "$ran" -eq 0 ]; then
  echo "macos-units.sh: no $kind unit in: $UNITS" >&2
  exit 2
fi
exit "$status"
