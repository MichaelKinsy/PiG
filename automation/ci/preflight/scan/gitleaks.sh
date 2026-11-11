#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Scan step: secrets in the lines the PR adds. Usage: gitleaks.sh <work dir> <main tree>
# Writes <work>/gitleaks.txt; exits 0 when clean. The rules and the ignore list are main's, never the PR's.
set -uo pipefail
work=$1 root=$2
(cd "$work" && gitleaks dir "$work/added" --config "$root/.gitleaks.toml" --gitleaks-ignore-path "$root" \
	--no-banner --redact --exit-code 1 --report-format json --report-path "$work/gitleaks.json") > "$work/gitleaks.log" 2>&1
status=$?
if [ -s "$work/gitleaks.json" ]; then
	jq -r '.[] | "\(.RuleID) in the added lines of \(.File | sub(".*/added/"; "")):\(.StartLine)"' "$work/gitleaks.json" > "$work/gitleaks.txt"
else
	tail -3 "$work/gitleaks.log" > "$work/gitleaks.txt"
fi
exit "$status"
