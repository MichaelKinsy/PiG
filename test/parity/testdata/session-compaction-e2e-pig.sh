#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestAgentSessionCompactionE2EUpstream$' -v ./coding); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^COMPACTION_E2E '
