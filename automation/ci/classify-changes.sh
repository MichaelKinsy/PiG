#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
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

product=true
if [ "$event" = pull_request ] || [ "$event" = merge_group ]; then
  : "${base:?base commit}" "${head:?head commit}"
  # A failing diff must fail the job, not read as an empty change that skips the product suites.
  changed=$(git diff --name-only "$base...$head")
  product=false
  while IFS= read -r path; do
    [ -n "$path" ] || continue
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
