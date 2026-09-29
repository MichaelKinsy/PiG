#!/usr/bin/env bash
set -euo pipefail
output=$(PIG_REPLACED_2860_PROBE=1 go test ./coding -run '^(TestReplacedSession2860|TestReplacedContextUserMessageAwaitsTheTurn)' -count=1 -v 2>&1) || { printf '%s\n' "$output"; exit 1; }
for site in 147 211 243; do
  printf '%s\n' "$output" | grep "^RUNTIME_ORIGINAL \[\"replaced\",$site,"
done
