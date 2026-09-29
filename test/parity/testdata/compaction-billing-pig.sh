#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^Test(InteractiveCompactionUpstream|CompactionBillingRenderProbe)$' -v ./internal/codingagent); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^COMPACTION_BILLING '
