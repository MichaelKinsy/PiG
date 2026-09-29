#!/usr/bin/env bash
set -euo pipefail
log=$(mktemp)
trap 'rm -f "$log"' EXIT
if ! go test ./internal/codingagent -run '^TestInMemorySettingsReloadUpstream$' -count=1 -v >"$log" 2>&1; then
  tail -100 "$log" >&2
  exit 1
fi
grep '^SETTINGS_MEMORY ' "$log"
