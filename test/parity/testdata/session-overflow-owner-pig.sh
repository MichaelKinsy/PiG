#!/usr/bin/env bash
set -euo pipefail
output=$(PIG_BOUNDARY_PROBE=1 ./test/parity/testdata/prebuilt-go-test.sh ./coding -run "^TestExtensionOwnedCompactionContinuesAfterOverflow$" -count=1 -v 2>&1) || { printf "%s\n" "$output"; exit 1; }
printf "%s\n" "$output" | grep "^OVERFLOW_OWNER_CASE "
