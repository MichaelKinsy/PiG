#!/usr/bin/env bash
# Fail if parity coverage regressed: fewer covered entries than HEAD,
# or any previously-passing scenario now fails.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

CUR=test/parity/coverage.md
if [[ ! -f "$CUR" ]]; then
  echo "release-check: test/parity/coverage.md missing: run 'make coverage' first"
  exit 1
fi

# The behavioral count of the report in file $1, from the status line test/parity/cmd/coverage writes:
# "- **Behavior verification:** N / M ported entries ...". Empty when the report has none. sed reads the file itself:
# a pipe into a sed that quits early would end the writer with SIGPIPE, which pipefail turns into an exit.
behavioral_count() {
  sed -nE '/^- \*\*Behavior verification:\*\* [0-9]+ \/ [0-9]+ ported entries/{s/^- \*\*Behavior verification:\*\* ([0-9]+) .*/\1/p;q;}' "$1"
}

cur_covered=$(behavioral_count "$CUR")
if [[ -z "$cur_covered" ]]; then
  echo "release-check: $CUR has no behavioral count (\"**Behavior verification:** N / M ported entries\")"
  exit 1
fi
previous=$(mktemp "${TMPDIR:-/tmp}/pig.XXXXXXXX")
trap 'rm -f "$previous"' EXIT
git show HEAD:test/parity/coverage.md >"$previous" 2>/dev/null || :
prev_covered=$(behavioral_count "$previous")
prev_covered=${prev_covered:-0}

if (( cur_covered < prev_covered )); then
  echo "release-check: coverage regressed ($prev_covered -> $cur_covered)"
  exit 1
fi

# Any "fail" cell in the current report = release-blocker.
if grep -q '\*\*[0-9]\+ fail\*\*' "$CUR"; then
  echo "release-check: failing scenarios in test/parity/coverage.md"
  grep '\*\*[0-9]\+ fail\*\*' "$CUR"
  exit 1
fi

echo "release-check: coverage OK ($cur_covered covered, no failures)"
