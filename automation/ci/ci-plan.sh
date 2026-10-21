#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Prints the CI plan for one event as GITHUB_OUTPUT lines, from automation/ci/ci-tiers.txt:
#
#   full=true|false          the full run (every row), or the light pull-request run (the pr rows)
#   linux, windows, macos=   JSON matrix include lists of {"shard": ...} (linux rows add "target": the make target)
#   run-<job>=true|false     whether each single (non-matrix) job runs
#
# The light run is a pull_request while LIGHT is "true"; every other event, and a pull_request without LIGHT, is the full run.
#
# Usage: ci-plan.sh <event> <light> [tiers-file]
set -euo pipefail

event=${1:?event}
light=${2-}
table=${3:-$(dirname "$0")/ci-tiers.txt}

full=true
if [ "$event" = pull_request ] && [ "$light" = true ]; then
  full=false
fi

echo "full=$full"
awk -F'|' -v full="$full" '
  function trim(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
  /^[ \t]*(#|$)/ { next }
  {
    job = trim($1); shard = trim($2); tier = trim($3); light_target = trim($4)
    if (job !~ /^(linux|windows|macos|single)$/ || shard == "" || tier !~ /^(pr|queue)$/ || NF < 3 || NF > 4) {
      printf "ci-tiers.txt line %d: want \"job | shard | pr|queue [| make target]\", got: %s\n", NR, $0 > "/dev/stderr"; bad = 1; next
    }
    if (light_target != "" && job != "linux") {
      printf "ci-tiers.txt line %d: only a linux row has a light-run make target\n", NR > "/dev/stderr"; bad = 1; next
    }
    if (shard ~ /["\\]/) { printf "ci-tiers.txt line %d: shard has a quote or backslash\n", NR > "/dev/stderr"; bad = 1; next }
    if (seen[job SUBSEP shard]++) { printf "ci-tiers.txt line %d: duplicate row %s | %s\n", NR, job, shard > "/dev/stderr"; bad = 1; next }
    runs = (full == "true" || tier == "pr") ? 1 : 0
    if (job == "single") { single[shard] = runs; singles[++nsingle] = shard; next }
    if (!runs) next
    if (job == "linux") {
      target = (full == "false" && light_target != "") ? light_target : "ci-" shard
      entry = "{\"shard\":\"" shard "\",\"target\":\"" target "\"}"
    } else {
      entry = "{\"shard\":\"" shard "\"}"
    }
    if (job in list) list[job] = list[job] ","
    list[job] = list[job] entry
  }
  END {
    if (bad) exit 1
    n = split("linux windows macos", jobs, " ")
    for (i = 1; i <= n; i++) {
      if (!(jobs[i] in list)) { printf "ci-tiers.txt: no %s row runs in this plan\n", jobs[i] > "/dev/stderr"; exit 1 }
      printf "%s=[%s]\n", jobs[i], list[jobs[i]]
    }
    for (i = 1; i <= nsingle; i++) printf "run-%s=%s\n", singles[i], (single[singles[i]] == 1 ? "true" : "false")
  }
' "$table"
