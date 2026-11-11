#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Scan step: the Pi file a `Pi-Behavior-Change: <file.ts:line>` citation names is a real Pi source, one the inventories
# of main name (docs/parity/PORT_MAP.md, the test mappings, the upstream sync tables). Usage: test-expectation.sh <work dir> <main tree>
# Reads the citations from <work>/data.json (PR body and commit messages); writes <work>/test-expectation.txt.
set -uo pipefail
work=$1 root=$2
mapfile -t cited < <(jq -r '[.pull.body, .commits[].commit.message] | .[]' "$work/data.json" \
	| grep -oE '^Pi-Behavior-Change:[[:space:]]*[^[:space:]]+\.ts:[0-9]+' | sed -E 's/^[^:]+:[[:space:]]*//; s/:[0-9]+$//' | sort -u)
known=$(cd "$root" && cat docs/parity/PORT_MAP.md test/parity/interfaces/test-mapping-v*.json test/parity/upstream-sync/*.toml 2>/dev/null \
	| grep -oE 'packages/[A-Za-z0-9_./-]+\.ts\b' | xargs -r -n1 basename | sort -u)
: > "$work/test-expectation.txt"
for path in "${cited[@]}"; do
	grep -qxF "$(basename "$path")" <<< "$known" || echo "Pi-Behavior-Change cites $path, which no inventory of main names as a Pi source" >> "$work/test-expectation.txt"
done
[ ! -s "$work/test-expectation.txt" ]
