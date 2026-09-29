#!/usr/bin/env bash
set -euo pipefail
PIG_PT_SHUTDOWN_PROBE=1 go test ./internal/codingagent -run '^TestUpstreamSignalShutdownExtensionCleanup$' -count=1 -v | grep '^SHUTDOWN_ORDER '
