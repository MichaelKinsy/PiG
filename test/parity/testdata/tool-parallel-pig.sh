#!/usr/bin/env bash
set -euo pipefail
if ! output=$(./test/parity/testdata/prebuilt-go-test.sh -count=1 -run '^TestSessionCustomToolsDefaultToParallel$' -v ./coding); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^PARALLEL_TOOLS '
