#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Nightly race and goroutine-leak gate for the packages that own concurrency: the agent loop and its state lock (agent), the provider streams (ai), the terminal
# loop (tui, internal/codingagent), the durable harness, the durable core's driver and its SQLite host (the driver's tests run the reference Harness and the
# core, whose owner goroutine steps the core while host callbacks run), the extension hosts and SDK (coding/extension/..., extensions/sdk), the session core
# (coding), the RPC/print/JSON modes (coding/cli) and the libraries that run goroutines of their own. automation/ci/race-packages.txt names every package and
# its group; race-coverage.sh fails when a concurrency owner is missing from it. A package whose TestMain calls testenv.RunWithoutGoroutineLeaks also fails here
# when its tests leak a blocked goroutine.
# PIG_RACE_GROUP picks a share for one nightly job (ai, rest, host, coding, cli, libs; each is a job of the nightly matrix), or every package when unset.
# A path with its own go.mod runs as that module (GOWORK=off), as `make test-sdk-go` does.
# PIG_RACE_SHARD=<k>/<n> runs shard k of n of the tests of a group that is one package (automation/ci/test-shard-pattern.sh), for the cli group.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
group="${PIG_RACE_GROUP:-all}"
list="$root/automation/ci/race-packages.txt"

packages=()
modules=()
matched=0
while read -r entry_group path _; do
  [[ -z "${entry_group:-}" || "$entry_group" == \#* ]] && continue
  [[ "$entry_group" == exempt ]] && continue
  if [[ "$group" != all && "$group" != "$entry_group" ]]; then continue; fi
  matched=1
  if [[ -f "$root/$path/go.mod" ]]; then modules+=("$path"); else packages+=("$path"); fi
done <"$list"
if [[ $matched -eq 0 ]]; then echo "test-race-nightly: unknown PIG_RACE_GROUP ${group}" >&2; exit 2; fi

cd "$root"
timeout="${PIG_RACE_TIMEOUT:-60m}"
run=()
shard="${PIG_RACE_SHARD:-}"
if [[ -n "$shard" ]]; then
  if [[ ! "$shard" =~ ^[1-9][0-9]*/[1-9][0-9]*$ || ${#packages[@]} -ne 1 || ${#modules[@]} -ne 0 ]]; then
    echo "test-race-nightly: PIG_RACE_SHARD=<k>/<n> needs a group of exactly one package (group ${group}, shard ${shard})" >&2
    exit 2
  fi
  run=(-run "$(automation/ci/test-shard-pattern.sh "${shard%/*}" "${shard#*/}" "${packages[0]}")")
fi
if [[ ${#packages[@]} -gt 0 ]]; then
  go test -race -count=1 -timeout "$timeout" "${run[@]+"${run[@]}"}" "${packages[@]}"
fi
for module in "${modules[@]+"${modules[@]}"}"; do
  (cd "$module" && GOWORK=off go test -race -count=1 -timeout "$timeout" ./...)
done
echo "test-race-nightly: no data race in ${packages[*]+"${packages[*]}"} ${modules[*]+"${modules[*]}"}."
