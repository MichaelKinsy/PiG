#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Scan step: secrets that still work, in the lines the PR adds. Usage: trufflehog.sh <work dir> <main tree>
# Writes <work>/trufflehog.txt; exits 0 when clean. TRUFFLEHOG_FLAGS defaults to --no-update (a pinned binary never
# updates itself); set it empty where the binary's wrapper already passes it.
set -uo pipefail
work=$1
read -r -a extra <<< "${TRUFFLEHOG_FLAGS---no-update}"
trufflehog filesystem "$work/added" --only-verified --fail --json "${extra[@]}" > "$work/trufflehog.json" 2> "$work/trufflehog.log"
status=$?
jq -r 'select(.DetectorName) | "\(.DetectorName) verified secret in the added lines of \(.SourceMetadata.Data.Filesystem.file | sub(".*/added/"; ""))"' "$work/trufflehog.json" > "$work/trufflehog.txt" 2>/dev/null
[ "$status" -eq 0 ] || [ -s "$work/trufflehog.txt" ] || tail -3 "$work/trufflehog.log" > "$work/trufflehog.txt"
exit "$status"
