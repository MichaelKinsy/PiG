#!/usr/bin/env bash
set -euo pipefail
if ! output=$(./test/parity/testdata/prebuilt-go-test.sh -count=1 -run '^TestExtensionActiveToolsNextTurnPort$' -v ./coding); then
    printf '%s\n' "$output"
    exit 1
fi
printf '%s\n' "$output" | grep '^NEXT_TOOLS '
