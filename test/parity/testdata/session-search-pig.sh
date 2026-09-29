#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^Test(UpstreamSessionSelectorSearch|SessionSearchRetainsAndClampsSelection)$' -v ./internal/codingagent); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep -E '^SESSION_(SEARCH|SELECTION) '
if ! output=$(go test -count=1 -run '^TestFuzzyMatchUnicodeScores$' -v ./tui); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^FUZZY_UNICODE '
