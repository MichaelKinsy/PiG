#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Nightly race and goroutine-leak gate for the packages that own concurrency: the agent loop and its state lock (agent), the provider streams (ai), the terminal
# loop (tui, internal/codingagent), the durable harness and the subprocess extension host. A package whose TestMain calls
# testenv.RunWithoutGoroutineLeaks also fails here when its tests leak a blocked goroutine.
set -euo pipefail

packages=(./agent ./ai ./tui ./internal/codingagent ./durable/harness ./coding/extension/host/subprocess)

go test -race -count=1 -timeout "${PIG_RACE_TIMEOUT:-60m}" "${packages[@]}"
echo "test-race-nightly: no data race in ${packages[*]}."
