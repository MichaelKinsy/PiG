#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Finds the first commit of a range that fails the probe, with git bisect, and prints it as "first-bad=<sha>".
#
# Usage: nightly-bisect.sh <good-sha> <bad-sha>
# Environment: PROBE, the command run with the commit under test as its one argument: exit 0 for good, 1 for bad, 125 for "cannot tell" (default
# automation/ci/nightly-probe.sh, which runs CI on the commit). The checkout needs the history between the two commits.
#
# The nightly red CI run is the bad end and the last green nightly the good one. A range of one commit needs no probe.
set -euo pipefail

good=${1:?good sha}
bad=${2:?bad sha}
probe=${PROBE:-$(dirname "$0")/nightly-probe.sh}

git merge-base --is-ancestor "$good" "$bad" || {
  echo "::error::$good is not an ancestor of $bad; nothing to bisect" >&2
  exit 2
}
good=$(git rev-parse "$good^{commit}")
bad=$(git rev-parse "$bad^{commit}")
if [ "$good" = "$bad" ]; then
  echo "::error::the last green nightly and the red one name the same commit $bad: the failure is not a change of the code (flaky or infrastructure)" >&2
  exit 3
fi

count=$(git rev-list --count --first-parent "$good..$bad")
echo "bisecting $count commit(s) between $good and $bad" >&2
if [ "$count" = 1 ]; then
  echo "first-bad=$bad"
  exit 0
fi

log=$(mktemp)
trap 'git bisect reset >/dev/null 2>&1 || true; rm -f "$log"' EXIT
git bisect start --first-parent "$bad" "$good" >&2
# shellcheck disable=SC2016 # The inner sh expands $1 and the rev-parse substitution, not this shell.
if ! git bisect run sh -c 'exec "$1" "$(git rev-parse HEAD)"' sh "$probe" >"$log" 2>&1; then
  cat "$log" >&2
  echo "::error::the bisect did not finish (the probe could not tell good from bad)" >&2
  exit 4
fi
cat "$log" >&2
# A finished search leaves the first bad commit in refs/bisect/bad (the trap resets it). Its report line is not parsed: git 2.56 words it
# "is the first 'bad' commit" where older versions print "is the first bad commit".
first=$(git rev-parse --verify --quiet "refs/bisect/bad^{commit}") || {
  echo "::error::git bisect named no first bad commit" >&2
  exit 4
}
echo "first-bad=$first"
