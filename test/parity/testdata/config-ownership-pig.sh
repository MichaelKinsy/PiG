#!/usr/bin/env bash
set -euo pipefail
log=$(mktemp)
trap 'rm -f "$log"' EXIT
if ! ./test/parity/testdata/prebuilt-go-test.sh ./internal/codingagent -run '^(TestConfigNpmSelfUpdateCommandsOriginal|TestConfigOtherManagerSelfUpdateCommandsOriginal|TestConfigPnpmStoreEntrypointOwnershipOriginal|TestConfigReadOnlyPackageRootRejectsSelfUpdateOriginal)$' -count=1 -v >"$log" 2>&1; then tail -100 "$log" >&2; exit 1; fi
grep '^CONFIG_' "$log"
