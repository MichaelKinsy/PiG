#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Prints the commit range a pull request's secret scan covers: <merge-base of BASE and HEAD>..HEAD, the commits the
# pull request adds over its base branch. The range reaches back only to where the pull request left its base, so a
# batch of merged member branches cut from an older main does not rescan the main history it merges past.
#   secret-scan-range.sh <base-commit> <head-commit>
#
# Fails closed. The one thing printed on stdout, and only on exit 0, is "<full sha>..<full sha>". When the range cannot be
# computed (a missing argument, a ref that is not a commit, no merge base because the histories are unrelated or the clone is
# too shallow to reach it) nothing is printed on stdout, one line goes to stderr and the exit status is 2, so a caller
# that captures stdout can never scan a partial range. When the pull request adds nothing (the head is the base or already
# in it) the range is empty and both shas are equal: that is the answer "nothing to scan", which the caller tells from a
# failure by the exit status.
set -euo pipefail

fail() {
  printf 'secret-scan-range.sh: %s\n' "$*" >&2
  exit 2
}

[ "$#" -eq 2 ] || fail "usage: secret-scan-range.sh <base-commit> <head-commit>"
[ -n "$1" ] || fail "the base commit is empty"
[ -n "$2" ] || fail "the head commit is empty"

# commit_of <name> <revision>: prints the full sha of the commit the revision names, or ends the script.
commit_of() {
  local sha
  sha=$(git rev-parse --verify --quiet --end-of-options "$2^{commit}" 2>/dev/null) || fail "the $1 '$2' is not a commit in this repository (is the clone too shallow to hold it?)"
  [[ $sha =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]] || fail "the $1 '$2' resolved to '$sha', not a full commit sha"
  printf '%s\n' "$sha"
}

base=$(commit_of base "$1")
head=$(commit_of head "$2")
merge_base=$(git merge-base "$base" "$head" 2>/dev/null) || fail "no merge base of $base and $head: unrelated histories, or a clone too shallow to reach it"
[[ $merge_base =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]] || fail "the merge base of $base and $head is '$merge_base', not a full commit sha"
printf '%s..%s\n' "$merge_base" "$head"
