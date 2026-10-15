#!/usr/bin/env bash
set -euo pipefail
output=$(node --experimental-import-meta-resolve ./test/parity/testdata/session-runtime-pi.mjs notify 2>&1) || { printf '%s\n' "$output"; exit 1; }
# The adapter executes all original assertions. This scenario compares only the three reload cases; Session24 owns resource ordering.
for site in 386 419 443; do
  printf '%s\n' "$output" | grep "^RUNTIME_ORIGINAL \[\"notify\",$site,"
done
