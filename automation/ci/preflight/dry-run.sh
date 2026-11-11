#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Runs the pre-flight for one or more PRs locally, reading only: no status, no comment.
#   automation/ci/preflight/dry-run.sh <owner/repo> <work dir> <pr> [pr ...]
# PREFLIGHT_FORCE_SCANS=1 runs the scan steps even when a cheap check failed. Needs GH_TOKEN (or GITHUB_TOKEN), and
# gitleaks, trufflehog, reuse, actionlint and zizmor on PATH. The states stay in <work dir>/states, so a second run
# shows the reuse.
set -uo pipefail

repo=${1:?owner/repo} work=${2:?work dir}
shift 2
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)
bin="$work/bin"
mkdir -p "$bin"
(cd "$root" && go build -o "$bin/preflight" ./automation/ci/preflight && go build -o "$bin/fixtureguard" ./automation/ci/fixtureguard && go build -o "$bin/testprune" ./test/parity/cmd/testprune) || exit 2
export PREFLIGHT_ROOT="$root" PREFLIGHT_BIN_DIR="$bin" PREFLIGHT_DRY_RUN=1
exec "$root/automation/ci/preflight/batch.sh" "$repo" "$work/states" "$work/runs" "$@"
