#!/usr/bin/env bash
set -euo pipefail
output=$(PIG_CUSTOM_SETTLED_PROBE=1 ./test/parity/testdata/prebuilt-go-test.sh ./coding -run '^TestSettledCustomMessagePrecedesSettlementPublication$' -count=1 -v 2>&1) || { printf '%s\n' "$output"; exit 1; }
printf '%s\n' "$output" | grep '^CUSTOM_SETTLEMENT '
