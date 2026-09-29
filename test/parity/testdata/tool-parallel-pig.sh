#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestSessionCustomToolsDefaultToParallel$' -v ./coding); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^PARALLEL_TOOLS '
