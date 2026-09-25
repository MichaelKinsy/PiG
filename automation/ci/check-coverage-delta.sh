#!/usr/bin/env bash
# Fail if parity coverage regressed: fewer covered entries than HEAD,
# or any previously-passing scenario now fails.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

CUR=parity/coverage.md
if [[ ! -f "$CUR" ]]; then
  echo "release-check: parity/coverage.md missing: run 'make coverage' first"
  exit 1
fi

# Extract the behavioral verification count from the summary line.
# Old format: "57 covered by scenarios"
# New format: "Verification: 176 behavioral"
extract_behavioral_count() {
  local src="$1"
  local n
  n=$(printf '%s\n' "$src" | grep -oE '[0-9]+ covered by scenarios' | grep -oE '^[0-9]+' | head -1 || true)
  if [[ -n "$n" ]]; then
    printf '%s\n' "$n"
    return
  fi
  n=$(printf '%s\n' "$src" | grep -oE 'Verification:\*\* [0-9]+ behavioral|Verification:\s*[0-9]+ behavioral|\*\*Verification:\*\* [0-9]+ behavioral' | grep -oE '[0-9]+' | head -1 || true)
  if [[ -n "$n" ]]; then
    printf '%s\n' "$n"
    return
  fi
  printf '0\n'
}

cur_covered=$(extract_behavioral_count "$(cat "$CUR")")
prev_covered=$(extract_behavioral_count "$(git show HEAD:parity/coverage.md 2>/dev/null || true)")

if (( cur_covered < prev_covered )); then
  echo "release-check: coverage regressed ($prev_covered -> $cur_covered)"
  exit 1
fi

# Any "fail" cell in the current report = release-blocker.
if grep -q '\*\*[0-9]\+ fail\*\*' "$CUR"; then
  echo "release-check: failing scenarios in parity/coverage.md"
  grep '\*\*[0-9]\+ fail\*\*' "$CUR"
  exit 1
fi

echo "release-check: coverage OK ($cur_covered covered, no failures)"
