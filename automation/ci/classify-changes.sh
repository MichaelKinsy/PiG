#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Prints os-specific=true|false as well: true when a changed path matches a line of automation/ci/os-specific-paths.txt, a
# change a Linux run cannot prove, so the pull request runs the full run (Windows and macOS included).
#
# Prints product=true|false for a pull request or a merge group: false only when
# every changed path is project prose or repository metadata that no product test
# reads. Scheduled, manual and push runs always verify the product. A merge
# group's base is the commit its entry was queued on, so the diff is that one
# entry's change, the same change its pull request was classified on.
#
# Usage: classify-changes.sh <event> <base-sha> <head-sha>
set -euo pipefail

event=${1:?event}
base=${2:-}
head=${3:-}

os_paths=$(dirname "$0")/os-specific-paths.txt
os_specific=false
product=true
if [ "$event" = pull_request ] || [ "$event" = merge_group ]; then
  : "${base:?base commit}" "${head:?head commit}"
  # A failing diff must fail the job, not read as an empty change that skips the product suites.
  # --no-renames: a rename is listed as the path that disappears and the path that appears, whatever diff.renames says.
  changed=$(git diff --name-only --no-renames "$base...$head")
  product=false
  patterns=()
  while IFS= read -r line; do
    case "$line" in '' | '#'*) ;; *) patterns+=("$line") ;; esac
  done <"$os_paths"
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    for pattern in "${patterns[@]}"; do
      # shellcheck disable=SC2254 # the pattern is a glob on purpose
      case "$path" in
        $pattern)
          os_specific=true
          echo "os-specific change: $path" >&2
          break
          ;;
      esac
    done
    case "$path" in
      README.md | CHANGELOG.md | .github/CONTRIBUTING.md | .github/CODE_OF_CONDUCT.md | docs/project/GOVERNANCE.md | docs/project/MAINTAINERS.md | .github/SECURITY.md | .github/SUPPORT.md | CITATION.cff) ;;
      docs/media/* | .github/ISSUE_TEMPLATE/* | .github/pull_request_template.md | .github/CODEOWNERS) ;;
      .github/dependabot.yml | .github/security-insights.yml | .coderabbit.yaml) ;;
      *)
        product=true
        echo "product change: $path" >&2
        ;;
    esac
  done <<<"$changed"
fi
echo "product=$product"
echo "os-specific=$os_specific"
