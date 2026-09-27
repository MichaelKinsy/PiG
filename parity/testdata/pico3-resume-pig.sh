#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestResumeSnapshotsOrphansBeforeReturning$' -v ./agent/harness/pico3); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^PICO3_RESUME '
