#!/usr/bin/env bash
set -euo pipefail
PIG_PT_SHUTDOWN_PROBE=1 ./test/parity/testdata/prebuilt-go-test.sh ./internal/codingagent -run '^TestUpstreamSignalShutdownExtensionCleanup$' -count=1 -v | grep '^SHUTDOWN_ORDER '
