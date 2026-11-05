#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# The cheap checks CI runs first, run locally the way CI runs them (make pre-push):
#   DCO sign-offs, source-hygiene, shellcheck, gitleaks, ruff, docs-drift, go vet, staticcheck SA*.
# CI runs source-hygiene on the pull request's merge ref (its diff is the change since the merge base) and the DCO and
# gitleaks checks over every commit of the pull request, so this script diffs against the merge base with BASE, not HEAD^.
#
# Usage: automation/ci/pre-push-gate.sh [base-ref]     (default: PIG_GATE_BASE, then origin/main)
# A missing tool fails the gate; it never skips silently. Shellcheck's version is CI's (ubuntu-24.04 apt: SHELLCHECK_CI_VERSION).
set -uo pipefail

cd "$(git rev-parse --show-toplevel)" || exit 2
SHELLCHECK_CI_VERSION=${SHELLCHECK_CI_VERSION:-0.9.0}
base_ref=${1:-${PIG_GATE_BASE:-origin/main}}
base=$(git merge-base HEAD "$base_ref") || { echo "pre-push-gate: no merge base with $base_ref" >&2; exit 2; }
head=$(git rev-parse HEAD)
failed=()

step() { # name command...
  local name=$1
  shift
  echo "== $name"
  if ! "$@"; then
    failed+=("$name")
  fi
}

need() { # tool install-hint
  command -v "$1" >/dev/null 2>&1 || { echo "pre-push-gate: $1 not found ($2)" >&2; return 1; }
}

shellcheck_ci() {
  need shellcheck "apt/brew install shellcheck" || return 1
  local have
  have=$(shellcheck --version | awk '/^version:/ {print $2}')
  [ "$have" = "$SHELLCHECK_CI_VERSION" ] || echo "pre-push-gate: warning: shellcheck $have, CI runs $SHELLCHECK_CI_VERSION; findings may differ" >&2
  find automation docs/site/public -type f -name '*.sh' -print0 | xargs -0 shellcheck
}

gitleaks_range() {
  need gitleaks "brew install gitleaks" || return 1
  gitleaks git --no-banner --redact --log-opts="$base..$head" .
}

ruff_sdk() {
  need ruff "pip install -r automation/ci/security-tools.txt" || return 1
  ruff check extensions/sdk-py
}

staticcheck_sa() {
  # shellcheck disable=SC2046
  go tool staticcheck -checks='SA*' $(go list ./... | grep -v '/third_party/')
}

step "DCO sign-offs ($base..$head)" ./automation/ci/check-dco.sh "$base" "$head"
step "source-hygiene (diff base $base)" go run ./test/parity/cmd/sourcehygiene -diff-base "$base"
step "shellcheck $SHELLCHECK_CI_VERSION" shellcheck_ci
step "gitleaks ($base..$head)" gitleaks_range
step "ruff" ruff_sdk
step "docs-drift" make -s docs-drift
step "go vet" go vet ./...
step "staticcheck SA*" staticcheck_sa

if [ "${#failed[@]}" -gt 0 ]; then
  printf 'pre-push-gate: FAILED: %s\n' "${failed[*]}" >&2
  exit 1
fi
echo "pre-push-gate: all checks passed"
