#!/usr/bin/env bash
set -euo pipefail
output=$(node --experimental-import-meta-resolve ./test/parity/testdata/session-runtime-pi.mjs notify 2>&1) || { printf '%s\n' "$output"; exit 1; }
# Session24/28 own the other original notification cases. No assertion result or event sequence is rewritten here.
for site in 238 275 330; do
  printf '%s\n' "$output" | grep "^RUNTIME_ORIGINAL \[\"notify\",$site,"
done
node --experimental-import-meta-resolve ./test/parity/testdata/session-runtime-pi.mjs rebind
