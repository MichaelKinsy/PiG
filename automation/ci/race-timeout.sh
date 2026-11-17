# SPDX-License-Identifier: MIT
# Sourced by test-race-nightly.sh and test-grouped.sh: the go test -timeout of a run under -race, in one place. A race run takes several times as long as a plain one, so go test's 10-minute default fails packages that pass.
# shellcheck shell=bash
PIG_RACE_TIMEOUT="${PIG_RACE_TIMEOUT:-60m}"
