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
# PORTMAP_REQUESTED (optional env var, default "true" — fail closed) controls whether the portmap-1..12 shards are included
# on a pull_request full run. Every non-pull_request event (merge_group, schedule, workflow_dispatch, push) always includes
# portmap-1..12 regardless of PORTMAP_REQUESTED.

# Optional DAG selector switch via PIG_CI_DAG (default: off):
#   off     (default, or unset) existing static ci-tiers.txt behavior, 100% unchanged.
#   shadow  computes the affected-package DAG plan (via automation/ci/cidag/cmd/cidag) into PIG_CI_DAG_PLAN_OUT (written
#           only when PIG_CI_DAG_PLAN_OUT is set) and optional GITHUB_STEP_SUMMARY, while emitting the exact static
#           ci-tiers.txt outputs with zero gating changes. Any selector failure logs a warning and falls back cleanly.
#   on      intersects the pull_request plan with the fail-closed DAG selector (still honoring global-invariant gates,
#           unowned -> full, >300 files -> full, non-pull_request -> full, and failing closed to the static table on any
#           selector error).
#
# DAG inputs (used only when PIG_CI_DAG is shadow or on):
#   PIG_CI_DAG_BASE         git base ref/SHA for the diff (e.g., github.event.pull_request.base.sha)
#   PIG_CI_DAG_HEAD         git head ref/SHA (default: HEAD)
#   PIG_CI_DAG_FILES_FROM   optional file listing changed paths (one per line) instead of git diff
#   PIG_CI_DAG_PLAN_OUT     optional path to write ci-plan.json (shadow mode writes a plan file only when this is set)
#   PIG_CI_DAG_BIN          optional prebuilt cidag binary (otherwise runs `go run ./automation/ci/cidag/cmd/cidag`)
#
# Usage: ci-plan.sh <event> <mode> <full-requested> [tiers-file]
set -euo pipefail

event=${1:?event}
mode=${2-}
requested=${3-}
table=${4:-$(dirname "$0")/ci-tiers.txt}
portmap_requested=${PORTMAP_REQUESTED:-true}

dag_mode=${PIG_CI_DAG:-off}

case "$mode" in
  "" | queue | label) ;;
  *)
    echo "PIG_PR_CI_LIGHT is \"$mode\", want empty, queue or label" >&2
    exit 1
    ;;
esac

case "$portmap_requested" in
  true | false) ;;
  *)
    echo "PORTMAP_REQUESTED is \"$portmap_requested\", want true or false" >&2
    exit 1
    ;;
esac

case "$dag_mode" in
  "" | off) dag_mode=off ;;
  shadow | on) ;;
  *)
    echo "PIG_CI_DAG is \"$dag_mode\", want off, shadow or on" >&2
    exit 1
    ;;
esac

resolve_caller_path() {
  local p="$1" caller_dir="$2"
  if [ -z "$p" ] || [ "$p" = "-" ]; then
    printf '%s' "$p"
    return 0
  fi
  case "$p" in
    /*) printf '%s' "$p" ;;
    *) printf '%s/%s' "$caller_dir" "$p" ;;
  esac
}

run_cidag() {
  local caller_dir root
  caller_dir=$(pwd)
  root=$(cd -- "$(dirname "$0")/../.." && pwd)
  local resolved_table resolved_files_from resolved_plan_out resolved_summary
  resolved_table=$(resolve_caller_path "$table" "$caller_dir")
  resolved_files_from=$(resolve_caller_path "${PIG_CI_DAG_FILES_FROM:-}" "$caller_dir")
  resolved_plan_out=$(resolve_caller_path "${PIG_CI_DAG_PLAN_OUT:-}" "$caller_dir")
  resolved_summary=$(resolve_caller_path "${GITHUB_STEP_SUMMARY:-}" "$caller_dir")
  local -a args=(
    -root "$root"
    -mode "$dag_mode"
    -event "$event"
    -light-mode "$mode"
    -tiers-file "$resolved_table"
  )
  [ "$requested" = true ] && args+=(-full-requested)
  if [ -n "$resolved_files_from" ]; then
    args+=(-files-from "$resolved_files_from")
  elif [ -n "${PIG_CI_DAG_BASE:-}" ]; then
    args+=(-base "$PIG_CI_DAG_BASE" -head "${PIG_CI_DAG_HEAD:-HEAD}")
  else
    return 1
  fi
  [ -n "$resolved_plan_out" ] && args+=(-plan-out "$resolved_plan_out")
  [ -n "$resolved_summary" ] && args+=(-summary "$resolved_summary")
  if [ -n "${PIG_CI_DAG_BIN:-}" ]; then
    local resolved_bin="$PIG_CI_DAG_BIN"
    case "$resolved_bin" in
      */*) resolved_bin=$(resolve_caller_path "$resolved_bin" "$caller_dir") ;;
    esac
    (cd "$root" && "$resolved_bin" "${args[@]}" "$@")
  else
    (cd "$root/automation/ci/cidag" && GOWORK=off go run ./cmd/cidag "${args[@]}" "$@")
  fi
}

if [ "$dag_mode" = on ]; then
  # In on mode, delegate GITHUB_OUTPUT emission to cidag (which validates ci-tiers.txt identically).
  # If the selector fails for any reason (missing base ref, go list failure, etc.), fail closed to the static planner below.
  if [ -n "${PIG_CI_DAG_FILES_FROM:-}" ] || [ -n "${PIG_CI_DAG_BASE:-}" ]; then
    if dag_output=$(run_cidag -github-output); then
      printf '%s\n' "$dag_output"
      exit 0
    fi
    echo "ci-plan.sh: PIG_CI_DAG=on selector failed; failing closed to static ci-tiers.txt plan" >&2
  else
    echo "ci-plan.sh: PIG_CI_DAG=on without PIG_CI_DAG_BASE or PIG_CI_DAG_FILES_FROM; failing closed to static ci-tiers.txt plan" >&2
  fi
elif [ "$dag_mode" = shadow ]; then
  # In shadow mode, compute and record the DAG plan without altering any GITHUB_OUTPUT line.
  if [ -n "${PIG_CI_DAG_FILES_FROM:-}" ] || [ -n "${PIG_CI_DAG_BASE:-}" ]; then
    run_cidag >/dev/null || echo "ci-plan.sh: PIG_CI_DAG=shadow selector failed (continuing with static plan)" >&2
  fi
fi

full=true
required=false
if [ "$event" = pull_request ] && [ -n "$mode" ]; then
  [ "$requested" = true ] || full=false
  [ "$mode" = label ] && required=true
fi

include_portmap=true
if [ "$event" = pull_request ] && [ "$portmap_requested" = false ]; then
  include_portmap=false
fi

echo "full=$full"
echo "full-required=$required"
awk -F'|' -v full="$full" -v include_portmap="$include_portmap" '
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
    if (job == "linux" && shard ~ /^portmap-/ && include_portmap == "false") runs = 0
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
      # Linux always runs: it is the one runner of the light run. Windows and macOS rows are all queue rows, so the light run has none.
      if (!(jobs[i] in list) && (jobs[i] == "linux" || full == "true")) { printf "ci-tiers.txt: no %s row runs in this plan\n", jobs[i] > "/dev/stderr"; exit 1 }
      printf "%s=[%s]\n", jobs[i], list[jobs[i]]
    }
    for (i = 1; i <= nsingle; i++) printf "run-%s=%s\n", singles[i], (single[singles[i]] == 1 ? "true" : "false")
  }
' "$table"
