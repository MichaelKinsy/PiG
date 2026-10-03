#!/usr/bin/env bash
set -euo pipefail
output=$(PIG_RUNTIME_ORIGINAL_PROBE=1 go test ./internal/codingagent -run '^(TestSessionStartNotifyOriginalReplacement|TestStartupRebindOriginalStaleCompletion)$' -count=1 -v 2>&1) || { printf '%s\n' "$output"; exit 1; }
for site in 240 277 332; do
  printf '%s\n' "$output" | grep "^RUNTIME_ORIGINAL \[\"notify\",$site,"
done
printf '%s\n' "$output" | grep '^RUNTIME_ORIGINAL \["rebind",23,'
