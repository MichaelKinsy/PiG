#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^Test(BuildSessionContextUpstream|RestoreSessionRuntimeStateUsesActiveBranchAssistantModel)$' -v ./internal/codingagent ./coding); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^SESSION_CONTEXT '
if ! output=$(go test -count=1 -run '^TestContextSettingsIgnoreUnrelatedEntryProperties$' -v ./internal/codingagent); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^SESSION_METADATA '
