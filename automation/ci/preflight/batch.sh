#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Runs the pre-flight over every open PR of a repository, a few at a time, on one runner.
#   batch.sh <owner/repo> <states dir> <work dir> [pr ...]
# Without PR numbers it takes every open PR. A PR whose head and base did not change since its last run costs one API
# call and is reused. One PR's failure does not stop the others; the exit status is 2 only when the pre-flight itself
# could not run for some PR. PREFLIGHT_JOBS sets how many PRs run at once (default 4). The environment of run-pr.sh
# (PREFLIGHT_ROOT, PREFLIGHT_BIN_DIR, PREFLIGHT_DRY_RUN, PREFLIGHT_FORCE_SCANS) passes through.
set -uo pipefail

repo=${1:?owner/repo} states=${2:?states dir} work=${3:?work dir}
shift 3
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
mkdir -p "$states" "$work"
if [ "$#" -gt 0 ]; then
	prs=("$@")
else
	mapfile -t prs < <(gh api --paginate "repos/$repo/pulls?state=open&per_page=100" --jq '.[].number')
fi
echo "pre-flight over ${#prs[@]} PR(s) of $repo, ${PREFLIGHT_JOBS:-4} at a time"

# shellcheck disable=SC2329 # xargs calls it through bash -c.
run_one() {
	local pr=$1
	PREFLIGHT_STATE="$states/$pr.json" "$here/run-pr.sh" "$repo" "$pr" "$work/$pr" > "$work/$pr.log" 2>&1
	echo "$pr $?" >> "$work/results.txt"
}
export -f run_one
export repo states work here
: > "$work/results.txt"
printf '%s\n' "${prs[@]}" | xargs -r -P "${PREFLIGHT_JOBS:-4}" -I{} bash -c 'run_one {}'

status=0
while read -r pr code; do
	case $code in
		0) result=pass ;;
		1) result="fail or open" ;;
		*) result="could not run"; status=2 ;;
	esac
	echo "::group::PR #$pr: $result"
	cat "$work/$pr.log"
	echo "::endgroup::"
done < <(sort -n "$work/results.txt")
exit "$status"
