#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestSessionSelectorDeleteRefreshImmediatelyRemovesSession$' -v ./internal/codingagent); then
  printf '%s\n' "$output" >&2
  exit 1
fi
printf '%s\n' "$output" | grep '^SESSION_DELETE_REFRESH '
