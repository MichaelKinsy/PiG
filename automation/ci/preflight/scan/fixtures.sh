#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Scan step: captured provider data and private terms (FG001-FG006) in the changed files. Usage: fixtures.sh <work dir> <main tree>
# main's fixture guard ($PREFLIGHT_BIN_DIR/fixtureguard) scans a repository that holds only the changed files at their head content. Its allowlist
# names files outside that subset as stale, so only findings ("path:line: FGnnn: ...") count. Writes <work>/fixtures.txt.
set -uo pipefail
work=$1
mapfile -t files < "$work/files.txt"
tree="$work/fixtures-tree"
rm -rf "$tree" && mkdir -p "$tree"
(cd "$work/head" && cp --parents -t "$tree" "${files[@]}" 2>/dev/null)
git -C "$tree" init -q && git -C "$tree" add -A
"$PREFLIGHT_BIN_DIR/fixtureguard" -root "$tree" > "$work/fixtures-all.txt" 2>&1
grep -E '^[^ ]+:[0-9]+: FG[0-9]{3}: ' "$work/fixtures-all.txt" > "$work/fixtures.txt"
[ ! -s "$work/fixtures.txt" ]
