#!/usr/bin/env bash
set -euo pipefail
output=$(PIG_RUNTIME_ORIGINAL_PROBE=1 go test ./coding ./internal/codingagent -run '^(TestAgentSessionRuntimeOriginalAssistantReplacement|TestSessionStartNotifyOriginalLoadedResources)$' -count=1 -v 2>&1) || { printf '%s\n' "$output"; exit 1; }
# Order independent cases by original suite, without changing observed values.
printf '%s\n' "$output" | grep '^RUNTIME_OBSERVATION \["runtime",'
printf '%s\n' "$output" | grep '^RUNTIME_OBSERVATION \["notify",'
