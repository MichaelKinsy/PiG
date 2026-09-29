#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestTaskkillSpawnFailurePort$' -v ./internal/codingagent/tools); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^TASKKILL '
