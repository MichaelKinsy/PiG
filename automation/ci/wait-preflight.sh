#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Waits for the "preflight" commit status of a pull request head (recorded by .github/workflows/preflight.yml, whose
# program is automation/ci/preflight) and exits with its verdict: 0 for success, 1 for failure, error, or no verdict
# within the time limit. The heavy jobs of ci.yml wait for this one, so a pull request the pre-flight rejects spends
# no minutes on the full jobs.
#
# Usage: wait-preflight.sh <owner/repo> <head-sha>
# Environment: GH_TOKEN (statuses: read); PREFLIGHT_CONTEXT (default preflight), PREFLIGHT_WAIT_SECONDS (default 900),
# PREFLIGHT_POLL_SECONDS (default 10), GH (the gh command, for tests).
set -euo pipefail

repo=${1:?owner/repo}
sha=${2:?head sha}
context=${PREFLIGHT_CONTEXT:-preflight}
limit=${PREFLIGHT_WAIT_SECONDS:-900}
poll=${PREFLIGHT_POLL_SECONDS:-10}
gh=${GH:-gh}

waited=0
while :; do
  # Statuses come newest first; the first of the context is its current state.
  state=$("$gh" api "repos/$repo/commits/$sha/statuses?per_page=100" \
    --jq "[.[] | select(.context == \"$context\")][0].state // \"none\"")
  case "$state" in
    success)
      echo "pre-flight passed for $sha"
      exit 0
      ;;
    failure | error)
      echo "::error::the pre-flight rejected $sha: see the pre-flight comment on the pull request"
      exit 1
      ;;
    none | pending) ;;
    *)
      echo "::error::unknown pre-flight state \"$state\" for $sha"
      exit 1
      ;;
  esac
  if [ "$waited" -ge "$limit" ]; then
    echo "::error::no pre-flight verdict for $sha within ${limit}s (state: $state); run the Pre-flight workflow for this pull request"
    exit 1
  fi
  sleep "$poll"
  waited=$((waited + poll))
done
