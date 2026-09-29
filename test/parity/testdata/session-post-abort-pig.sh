#!/usr/bin/env bash
set -euo pipefail
if ! output=$(go test -count=20 -run '^Test(AutomaticCompactionCancellationUpstream|SessionPublicListenersPrecedePersistenceAndTools)$' -v ./coding); then
    printf '%s\n' "$output"
    exit 1
fi
# Each run independently asserts the same complete outcome; retain every row.
printf '%s\n' "$output" | grep -E '^(POST_ABORT|LISTENER_ORDER) '
