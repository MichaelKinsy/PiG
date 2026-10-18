#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Nightly race and goroutine-leak gate for the packages that own concurrency: the agent loop and its state lock (agent), the provider streams (ai), the terminal
# loop (tui, internal/codingagent), the durable harness, the durable core's driver and its SQLite host (the driver's tests run the reference Harness and the
# core, whose owner goroutine steps the core while host callbacks run), and the subprocess extension host. A package whose TestMain calls
# testenv.RunWithoutGoroutineLeaks also fails here when its tests leak a blocked goroutine.
# PIG_RACE_GROUP picks a share for one nightly job: ai alone (its -race run is the longest, and beside the other seven
# binaries it shared the runner's cores), the rest, or every package when unset.
set -euo pipefail

case "${PIG_RACE_GROUP:-all}" in
ai) packages=(./ai) ;;
rest) packages=(./agent ./tui ./internal/codingagent ./durable/harness ./durable/core/driver ./durable/core/sqlhost ./coding/extension/host/subprocess) ;;
all) packages=(./agent ./ai ./tui ./internal/codingagent ./durable/harness ./durable/core/driver ./durable/core/sqlhost ./coding/extension/host/subprocess) ;;
*) echo "test-race-nightly: unknown PIG_RACE_GROUP ${PIG_RACE_GROUP}" >&2; exit 2 ;;
esac

go test -race -count=1 -timeout "${PIG_RACE_TIMEOUT:-60m}" "${packages[@]}"
echo "test-race-nightly: no data race in ${packages[*]}."
