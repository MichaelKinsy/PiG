#!/usr/bin/env bash
set -euo pipefail
output=$(PIG_SDK_MANAGER_PROBE=1 go test ./coding -run '^TestUpstreamSDKSessionManager' -count=1 -v 2>&1) || { printf '%s\n' "$output"; exit 1; }
for case_id in 1 2 3 4; do
  printf '%s\n' "$output" | grep "^SDK_SESSION_MANAGER \[$case_id,"
done
