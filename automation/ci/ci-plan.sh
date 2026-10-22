#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Prints the CI plan for one event as GITHUB_OUTPUT lines, from automation/ci/ci-tiers.txt:
#
#   full=true|false          the full run (every row), or the light pull-request run (the pr rows)
#   full-required=true|false the run's CI result passes only when the full run ran (label mode, pull request)
#   linux, windows, macos=   JSON matrix include lists of {"shard": ...} (linux rows add "target": the make target)
#   run-<job>=true|false     whether each single (non-matrix) job runs
#
# Every event but a pull request is the full run. A pull request is the full run when MODE is empty, and otherwise the light
# run unless FULL_REQUESTED is "true" (the pull request carries the ci:full label, or it was just marked ready for review):
#
#   MODE empty   no light run: every pull request runs the full set.
#   MODE queue   the merge queue runs the full set on merge_group, so the light run's CI result may pass.
#   MODE label   there is no merge queue: a light run's CI result fails (full-required=true), so only a full run on the
#                pull request's head can satisfy the required check.
#
# Usage: ci-plan.sh <event> <mode> <full-requested> [tiers-file]
set -euo pipefail

event=${1:?event}
mode=${2-}
requested=${3-}
table=${4:-$(dirname "$0")/ci-tiers.txt}

case "$mode" in
  "" | queue | label) ;;
  *)
    echo "PIG_PR_CI_LIGHT is \"$mode\", want empty, queue or label" >&2
    exit 1
    ;;
esac

full=true
required=false
if [ "$event" = pull_request ] && [ -n "$mode" ]; then
  [ "$requested" = true ] || full=false
  [ "$mode" = label ] && required=true
fi

echo "full=$full"
echo "full-required=$required"
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
