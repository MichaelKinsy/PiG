#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^Test(SessionFileInteropProbe|SessionCwdHandlingUpstream)$' -v ./internal/codingagent ./coding); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep -E '^SESSION_(FILE|CWD) '
