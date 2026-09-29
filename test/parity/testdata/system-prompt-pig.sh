#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^(TestUpstreamSystemPrompt|TestSystemPromptParityProbe)$' -v ./internal/codingagent/prompts); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^SYSTEM_PROMPT '
