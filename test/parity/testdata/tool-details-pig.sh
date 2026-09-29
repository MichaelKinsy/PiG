#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestBuiltinToolDetailsParityProbe$' -v ./internal/codingagent/tools); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^TOOL_WIRE '
