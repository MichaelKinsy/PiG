#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Opens (or finds) the issue that asks for a fix PR for the first bad commit of a red nightly. It never opens a pull
# request, never reverts and never merges: the author or a maintainer decides between a fix and a revert.
#
# Usage: nightly-issue.sh <first-bad-sha> <good-sha> <bad-sha> [red-run-url] [red-run-id]
# With the red run's id the issue lists that run's failed and timed-out jobs, each with its link.
# Environment: GH_TOKEN, REPO (owner/repo), GH (the gh command, for tests).
set -euo pipefail

first=${1:?first bad sha}
good=${2:?good sha}
bad=${3:?bad sha}
url=${4:-}
run=${5:-}
repo=${REPO:?owner/repo}
gh=${GH:-gh}

title="Nightly CI red: first bad commit ${first:0:12}"
existing=$("$gh" issue list --repo "$repo" --state open --search "\"$title\" in:title" --json number --jq '.[0].number // empty')
if [ -n "$existing" ]; then
  echo "issue #$existing already tracks ${first:0:12}"
  exit 0
fi

summary=$(git log -1 --format='%h %s (%an)' "$first")
failed=
if [ -n "$run" ]; then
  # The issue is opened without the jobs when they cannot be read: it is the request for the fix.
  if jobs=$("$gh" run view "$run" --repo "$repo" --json jobs \
    --jq '.jobs[] | select(.conclusion == "failure" or .conclusion == "timed_out") | "  - \(.name) (\(.conclusion)): \(.url)"'); then
    failed=$(printf '%s\n%s' "- failed jobs of the red run:" "${jobs:-  - none reported}")
  else
    failed="- failed jobs of the red run: could not be read; see the run"
  fi
fi
body=$(printf '%s\n' \
  "The nightly full CI run on main went red. Bisecting the commits between the last green nightly and the red one found:" \
  "" \
  "- first bad commit: \`$first\` — $summary" \
  "- last green nightly head: \`$good\`" \
  "- red nightly head: \`$bad\`" \
  "${url:+- red run: $url}" \
  "$failed" \
  "" \
  "Open a fix PR (or a revert PR) against main. Nothing here merges anything: this issue is the request, and the fix goes through the normal pre-flight, review and CI." \
  "" \
  "Each probed commit was one full CI run on a temporary \`bisect/*\` branch, deleted afterwards.")
"$gh" issue create --repo "$repo" --title "$title" --body "$body"
