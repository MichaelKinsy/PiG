#!/usr/bin/env bash
set -euo pipefail
# The Go probe lives next to the private interactive owner-loop implementation.
# Preserve its exit status; print only its machine-readable trace on success.
if ! output=$(go test -count=1 -run '^TestStatusLifecycleProbe$' -v ./internal/codingagent); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^STATUS_LIFECYCLE '
