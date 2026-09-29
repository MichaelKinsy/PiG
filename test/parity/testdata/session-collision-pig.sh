#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.."
go test ./internal/codingagent -run '^TestSessionEntryCollisionPairedProbe$' -count=1 -v |
  awk '/^SESSION_COLLISION / { print; found=1 } END { if (!found) exit 1 }'
