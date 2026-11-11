#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Opens (or finds) the issue that asks for a fix PR for the first bad commit of a red nightly. It never opens a pull
# request, never reverts and never merges: the author or a maintainer decides between a fix and a revert.
#
# Usage: nightly-issue.sh <first-bad-sha> <good-sha> <bad-sha> [red-run-url]
# Environment: GH_TOKEN, REPO (owner/repo), GH (the gh command, for tests).
set -euo pipefail

first=${1:?first bad sha}
good=${2:?good sha}
bad=${3:?bad sha}
url=${4:-}
repo=${REPO:?owner/repo}
gh=${GH:-gh}

title="Nightly CI red: first bad commit ${first:0:12}"
existing=$("$gh" issue list --repo "$repo" --state open --search "\"$title\" in:title" --json number --jq '.[0].number // empty')
if [ -n "$existing" ]; then
  echo "issue #$existing already tracks ${first:0:12}"
  exit 0
fi

summary=$(git log -1 --format='%h %s (%an)' "$first")
body=$(printf '%s\n' \
  "The nightly full CI run on main went red. Bisecting the commits between the last green nightly and the red one found:" \
  "" \
  "- first bad commit: \`$first\` — $summary" \
  "- last green nightly head: \`$good\`" \
  "- red nightly head: \`$bad\`" \
  "${url:+- red run: $url}" \
  "" \
  "Open a fix PR (or a revert PR) against main. Nothing here merges anything: this issue is the request, and the fix goes through the normal pre-flight, review and CI." \
  "" \
  "Each probed commit was one full CI run on a temporary \`bisect/*\` branch, deleted afterwards.")
"$gh" issue create --repo "$repo" --title "$title" --body "$body"
