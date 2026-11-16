#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Prints the commit range a pull request's secret scan covers: <merge-base of BASE and HEAD>..HEAD, the commits the
# pull request adds over its base branch. The range reaches back only to where the pull request left its base, so a
# batch of merged member branches cut from an older main does not rescan the main history it merges past.
#   secret-scan-range.sh <base-commit> <head-commit>
set -euo pipefail
base=${1:?usage: secret-scan-range.sh <base-commit> <head-commit>}
head=${2:?usage: secret-scan-range.sh <base-commit> <head-commit>}
printf '%s..%s\n' "$(git merge-base "$base" "$head")" "$(git rev-parse "$head")"
