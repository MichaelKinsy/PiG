#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# Prints product=true|false for a pull request: false only when every changed
# path is project prose or repository metadata that no product test reads.
# Scheduled, manual and push runs always verify the product.
#
# Usage: classify-changes.sh <event> <base-sha> <head-sha>
set -euo pipefail

event=${1:?event}
base=${2:-}
head=${3:-}

product=true
if [ "$event" = pull_request ]; then
  product=false
  while IFS= read -r path; do
    case "$path" in
      README.md | CHANGELOG.md | CONTRIBUTING.md | CODE_OF_CONDUCT.md | GOVERNANCE.md | MAINTAINERS.md | SECURITY.md | SUPPORT.md | CITATION.cff) ;;
      media/* | .github/ISSUE_TEMPLATE/* | .github/pull_request_template.md | .github/CODEOWNERS) ;;
      .github/dependabot.yml | .github/security-insights.yml | .coderabbit.yaml) ;;
      *)
        product=true
        echo "product change: $path" >&2
        ;;
    esac
  done < <(git diff --name-only "$base...$head")
fi
echo "product=$product"
