#!/usr/bin/env bash
set -euo pipefail
if ! output=$(node --experimental-import-meta-resolve ./test/parity/testdata/session-compaction-e2e-pi.mjs); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^COMPACTION_E2E '
