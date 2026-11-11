#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Runs the whole pre-flight for one PR: fetch, evaluate, prepare, the scan scripts in scan/, record.
#   run-pr.sh <owner/repo> <pr> <work dir>
# Environment:
#   PREFLIGHT_ROOT      main's tree (the code and configuration that judge the PR)
#   PREFLIGHT_BIN_DIR   holds preflight, fixtureguard and testprune, built from main
#   PREFLIGHT_STATE     the PR's state file, read as the previous run and written for the next
#   PREFLIGHT_DRY_RUN=1 set no status and post no comment
#   PREFLIGHT_FORCE_SCANS=1 run the scans even when a cheap check failed
# Exits 0 when the PR passes, 1 when it fails or a scan is still open, 2 when the pre-flight itself could not run.
set -uo pipefail

repo=${1:?owner/repo} pr=${2:?pr} work=${3:?work dir}
root=${PREFLIGHT_ROOT:?} bin=${PREFLIGHT_BIN_DIR:?} state=${PREFLIGHT_STATE:?}
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
mkdir -p "$work"
step() { echo "::group::PR #$pr: $*"; }
end() { echo "::endgroup::"; }

step fetch
"$bin/preflight" fetch --repo "$repo" --pr "$pr" --root "$root" --previous "$state" --out "$work/data.json" || { end; exit 2; }
end

step evaluate
"$bin/preflight" evaluate --data "$work/data.json" --out "$work/evaluation.json" --root "$root"
cheap=$?
end
[ "$cheap" -le 1 ] || exit 2

scans=1
if [ "$cheap" -eq 1 ] && [ "${PREFLIGHT_FORCE_SCANS:-}" != 1 ]; then
	echo "PR #$pr: scans skipped, a cheap check failed"
	scans=0
fi
if [ "$scans" = 1 ] && [ "$(jq '.scans | length' "$work/evaluation.json")" -gt 0 ]; then
	step prepare
	"$bin/preflight" prepare --data "$work/data.json" --eval "$work/evaluation.json" --dir "$work" || { end; exit 2; }
	end
	for name in $(jq -r '.scans[]' "$work/evaluation.json"); do
		step "scan $name"
		if [ -x "$here/scan/$name.sh" ]; then
			"$here/scan/$name.sh" "$work" "$root"
			status=$?
		else
			echo "no scan script scan/$name.sh" > "$work/$name.txt"
			status=2
		fi
		"$bin/preflight" verdict --eval "$work/evaluation.json" --check "$name" --status "$status" --report "$work/$name.txt"
		end
	done
fi

step record
dry=false
[ "${PREFLIGHT_DRY_RUN:-}" = 1 ] && dry=true
"$bin/preflight" record --data "$work/data.json" --eval "$work/evaluation.json" --state "$state" --dry-run "$dry" || { end; exit 2; }
end
result=$(jq -r .result "$work/evaluation.json")
echo "PR #$pr: pre-flight $result"
[ "$result" = pass ]
