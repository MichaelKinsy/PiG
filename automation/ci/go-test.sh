#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Usage: go-test.sh <go test arguments>
#
# Runs `go test <arguments>`. When PIG_TEST_JSON_DIR names a directory (CI sets it, and uploads the directory as the shard's
# test-json artifact), the run is `go test -json` instead: the raw events are appended to a file in that directory, which
# automation/ci/aggregate-test-timings.py reads, and the log gets the compact report of test-json-report.py (the whole text
# of the run when the arguments include -v). The exit status is go test's own, so what runs and what passes does not change.
set -euo pipefail

if [[ -z ${PIG_TEST_JSON_DIR:-} ]]; then
  exec go test "$@"
fi

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
mkdir -p "$PIG_TEST_JSON_DIR"
report=(--out "$PIG_TEST_JSON_DIR/$(date -u +%Y%m%dT%H%M%S)-$$.json")
for arg in "$@"; do
  [[ $arg == -args ]] && break
  [[ $arg == -v || $arg == -v=true || $arg == -test.v ]] && report+=(--verbatim)
done
# setup-python on Windows provides python, not always python3.
py=$(command -v python3 || command -v python)
go test -json "$@" | "$py" "$here/test-json-report.py" "${report[@]}"
