#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestSessionListingCancellationAndProgress$/rejects_a_cancelled_session_listing$' -v ./internal/codingagent); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^SESSION_LIST '
