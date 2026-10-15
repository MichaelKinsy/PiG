#!/usr/bin/env bash
set -euo pipefail
if ! output=$(./test/parity/testdata/prebuilt-go-test.sh -count=1 -run '^Test(InteractiveCompactionUpstream|CompactionBillingRenderProbe)$' -v ./internal/codingagent); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^COMPACTION_BILLING '
