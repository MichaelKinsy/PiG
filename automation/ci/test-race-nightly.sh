#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Nightly race and goroutine-leak gate for the packages that own concurrency: the agent loop and its state lock (agent), the provider streams (ai), the terminal
# loop (tui, internal/codingagent), the durable harness, the durable core's driver and its SQLite host (the driver's tests run the reference Harness and the
# core, whose owner goroutine steps the core while host callbacks run), the extension hosts and SDK (coding/extension/..., extensions/sdk), the session core
# (coding), the RPC/print/JSON modes (coding/cli) and the libraries that run goroutines of their own. automation/ci/race-packages.txt names every package and
# its group; race-coverage.sh fails when a concurrency owner is missing from it. A package whose TestMain calls testenv.RunWithoutGoroutineLeaks also fails here
# when its tests leak a blocked goroutine.
# PIG_RACE_GROUP picks a share for one nightly job (ai, rest, host, coding, cli, libs, hljs, photonimage, parity, shared; each is a job of the nightly matrix), or every package when unset.
# The -timeout of every run is PIG_RACE_TIMEOUT (default 60m), set in race-timeout.sh, which make test-changed's -race runs read too.
# A path with its own go.mod runs as that module (GOWORK=off), as `make test-sdk-go` does, unless its entry is marked `workspace`: a module that only builds inside a go.work (it needs its siblings' unreleased code) runs in the workspace its directory finds.
# PIG_RACE_SHARD=<k>/<n> (or space-separated list of <k>/<n> shards, e.g. "1/8 2/8") runs each listed shard of the tests of a group that is one package (automation/ci/test-shard-pattern.sh), each in its own go test process and timeout.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
group="${PIG_RACE_GROUP:-all}"
list="$root/automation/ci/race-packages.txt"

packages=()
modules=()
workspace_modules=()
matched=0
while read -r entry_group path marker; do
  [[ -z "${entry_group:-}" || "$entry_group" == \#* ]] && continue
  [[ "$entry_group" == exempt ]] && continue
  if [[ "$group" != all && "$group" != "$entry_group" ]]; then continue; fi
  matched=1
  if [[ -f "$root/$path/go.mod" ]]; then
    modules+=("$path")
    [[ "${marker:-}" == workspace ]] && workspace_modules+=("$path")
  else
    packages+=("$path")
  fi
done <"$list"
if [[ $matched -eq 0 ]]; then echo "test-race-nightly: unknown PIG_RACE_GROUP ${group}" >&2; exit 2; fi

cd "$root"
# shellcheck source=automation/ci/race-timeout.sh
source "$root/automation/ci/race-timeout.sh"
shard_spec="${PIG_RACE_SHARD:-}"
if [[ -n "$shard_spec" ]]; then
  read -r -a shards <<< "$shard_spec"
  if [[ ${#packages[@]} -ne 1 || ${#modules[@]} -ne 0 ]]; then
    echo "test-race-nightly: PIG_RACE_SHARD=<k>/<n> needs a group of exactly one package (group ${group}, shard ${shard_spec})" >&2
    exit 2
  fi
  for shard in "${shards[@]}"; do
    if [[ ! "$shard" =~ ^[1-9][0-9]*/[1-9][0-9]*$ ]]; then
      echo "test-race-nightly: PIG_RACE_SHARD=<k>/<n> needs a group of exactly one package (group ${group}, shard ${shard_spec})" >&2
      exit 2
    fi
  done
  status=0
  for shard in "${shards[@]}"; do
    echo "::group::${group} shard ${shard}"
    if pattern=$(automation/ci/test-shard-pattern.sh "${shard%/*}" "${shard#*/}" "${packages[0]}"); then
      go test -race -count=1 -timeout "$PIG_RACE_TIMEOUT" -run "$pattern" "${packages[0]}" || status=1
    else
      status=1
    fi
    echo "::endgroup::"
  done
  if [[ $status -ne 0 ]]; then
    exit "$status"
  fi
else
  if [[ ${#packages[@]} -gt 0 ]]; then
    go test -race -count=1 -timeout "$PIG_RACE_TIMEOUT" "${packages[@]}"
  fi
fi
for module in "${modules[@]+"${modules[@]}"}"; do
  if [[ " ${workspace_modules[*]+"${workspace_modules[*]}"} " == *" $module "* ]]; then
    (cd "$module" && go test -race -count=1 -timeout "$PIG_RACE_TIMEOUT" ./...)
  else
    (cd "$module" && GOWORK=off go test -race -count=1 -timeout "$PIG_RACE_TIMEOUT" ./...)
  fi
done
echo "test-race-nightly: no data race in ${packages[*]+"${packages[*]}"} ${modules[*]+"${modules[*]}"}."
