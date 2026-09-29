#!/usr/bin/env bash
set -euo pipefail
output=$(PIG_RUNTIME_REPLACEMENT_PROBE=1 go test ./coding -run '^(TestRuntimeOriginal|TestAgentSessionRuntimeOriginalAssistantReplacement)' -count=1 -v 2>&1) || { printf '%s\n' "$output"; exit 1; }
# These are independent original cases. Each marker is emitted only after its full production-path assertions pass.
for site in 125 166 216 249 294 329 378 391 431 543 548 620; do
  printf '%s\n' "$output" | grep "^RUNTIME_ORIGINAL \[\"runtime\",$site,"
done
printf '%s\n' "$output" | grep '^BRANCHING_ORACLE '
