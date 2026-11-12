#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# The probe of nightly-bisect.sh: runs ci.yml on one commit and reports the verdict by exit code (0 green, 1 red, 125 the
# run could not be judged). It pushes the commit to a temporary branch, because CI dispatches on a branch, waits for the
# run, and deletes the branch. The temporary branch is never a pull request and nothing merges it.
#
# Usage: nightly-probe.sh <sha>
# Environment: GH_TOKEN, REPO (owner/repo), BISECT_TAG (a run-unique string), PROBE_TIMEOUT_SECONDS (default 10800).
set -uo pipefail

sha=${1:?commit sha}
repo=${REPO:?owner/repo}
branch="bisect/${BISECT_TAG:?run tag}-${sha:0:12}"
limit=${PROBE_TIMEOUT_SECONDS:-10800}

# shellcheck disable=SC2329 # cleanup runs only through the EXIT trap below.
cleanup() { git push --quiet origin ":refs/heads/$branch" >/dev/null 2>&1 || true; }
trap cleanup EXIT

git push --quiet origin "$sha:refs/heads/$branch" || exit 125
gh workflow run ci.yml --repo "$repo" --ref "$branch" || exit 125

# The dispatch has no run id; the branch name is unique, so the one run on it is ours.
run=
for _ in $(seq 1 30); do
  run=$(gh run list --repo "$repo" --workflow ci.yml --branch "$branch" --event workflow_dispatch --limit 1 --json databaseId --jq '.[0].databaseId // empty') || run=
  [ -n "$run" ] && break
  sleep 10
done
[ -n "$run" ] || exit 125

waited=0
while [ "$waited" -lt "$limit" ]; do
  status=$(gh run view "$run" --repo "$repo" --json status,conclusion --jq '.status + " " + (.conclusion // "")') || status=
  case "$status" in
    "completed success") echo "$sha: green (run $run)"; exit 0 ;;
    "completed failure" | "completed timed_out") echo "$sha: red (run $run)"; exit 1 ;;
    "completed "*) echo "$sha: run $run ended $status, no verdict"; exit 125 ;;
  esac
  sleep 60
  waited=$((waited + 60))
done
gh run cancel "$run" --repo "$repo" >/dev/null 2>&1 || true
exit 125
