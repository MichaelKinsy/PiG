#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestManagedToolCallbacksProbe$' -v ./internal/codingagent/tools); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^MANAGED_TOOL_CALLBACKS '
if ! output=$(go test -count=1 -run '^TestManagedToolRenderProbe$' -v ./internal/codingagent); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^MANAGED_TOOL_RENDER '
