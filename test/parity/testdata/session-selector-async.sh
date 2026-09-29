#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=1 -run '^TestUpstreamSessionSelectorAsyncScopes$' -v ./internal/codingagent); then
  printf '%s\n' "$output" >&2
  exit 1
fi
printf '%s\n' "$output" | grep '^SESSION_PATH_DELETE_ASYNC '
