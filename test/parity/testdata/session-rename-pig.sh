#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestUpstreamSessionSelectorRename$' -v ./internal/codingagent); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^SESSION_RENAME '
if ! output=$(go test -count=1 -run '^Test(UpstreamInput.*|UpstreamWordNavigation.*|InputSetTextRetainsUTF16Cursor|InputRendererEncodesSplitUTF16Cursor)$' -v ./tui); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' 'INPUT_ORIGINAL_TESTS ok' 'WORD_ORIGINAL_TESTS ok'
printf '%s\n' "$output" | grep '^INPUT_UTF16 '
