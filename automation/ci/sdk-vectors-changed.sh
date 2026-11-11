#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Prints true when the committed diff against the base touches a path the SDK Pi vector suites read, and false otherwise.
#
# The Go, Rust, Python and Lisp SDKs keep their own copy of the shared tui engine (extensions/sdk/tui, a port of tui/) and test it against vectors
# generated from Pi (test/parity/sdk-vectors). The SDKs are separate modules, so the light pull-request run's test-changed (the main module's
# packages) never runs their tests; a change to tui/ or to a vector file broke them unnoticed until the full run. make sdk-vectors-changed uses
# this to run the vector check and the four SDK suites in the light run exactly when one of these paths changes.
#
# Usage: sdk-vectors-changed.sh [base-ref]    (base defaults to $TEST_BASE, then @{upstream}, then main)
set -euo pipefail

base=${1:-${TEST_BASE:-}}
if [ -z "$base" ]; then
  if git rev-parse --verify --quiet '@{upstream}^{commit}' >/dev/null; then base='@{upstream}'; else base=main; fi
fi
merge_base=$(git merge-base HEAD "$base")

result=false
while IFS= read -r path; do
  case "$path" in
    tui/* | extensions/sdk/* | extensions/sdk-rs/* | extensions/sdk-py/* | extensions/sdk-lisp/* | extensions/sdk-ts/* | internal/typebox/*) ;;
    test/parity/sdk-vectors/* | test/parity/sdk-bulk/* | automation/gen/generate-sdk-vectors.mjs | automation/ci/sdk-vectors-changed.sh | .upstream/pin) ;;
    *) continue ;;
  esac
  result=true
  echo "sdk vectors: $path" >&2
done < <(git diff --name-only --no-renames "$merge_base...HEAD")
echo "$result"
