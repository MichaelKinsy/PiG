#!/usr/bin/env bash
set -euo pipefail
if ! output=$(./test/parity/testdata/prebuilt-go-test.sh -count=1 -run '^TestSessionSelectorDeleteRefreshImmediatelyRemovesSession$' -v ./internal/codingagent); then
  printf '%s\n' "$output" >&2
  exit 1
fi
printf '%s\n' "$output" | grep '^SESSION_DELETE_REFRESH '
