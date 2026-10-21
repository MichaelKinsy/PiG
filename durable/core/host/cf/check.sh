#!/bin/sh
# Gates of the CF host: typecheck, then every test against the core built with each toolchain (default: go and tinygo).
# Usage: [TOOLCHAINS="go tinygo"] ./check.sh   (TinyGo: set TINYGO or put tinygo on PATH)
# Every toolchain runs even when an earlier one failed, so one run reports them all; the exit status is nonzero if any failed.
set -e
here=$(cd "$(dirname "$0")" && pwd)
cd "$here"
export GOTOOLCHAIN=${GOTOOLCHAIN:-go$(tr -d '\r\n' <"$here/../../../../.go-version")}
# The host's Pi packages are the pin's (a commit pin's build, or the pinned release), never whatever npm holds for the locked version.
python3 "$here/../../../../automation/pin/pin.py" check-durable-host durable/core/host/cf
node_modules/.bin/tsc -p .
status=0
for tc in ${TOOLCHAINS:-go tinygo}; do
  echo "== $tc"
  rm -f "dist/probe-$tc.wasm"
  # Test files read dist/core-$tc.wasm when they load, and run side by side: the module exists before any of them starts.
  ../../budget/build-wasm.sh "$tc" ../../cmd/corewasm "dist/core-$tc.wasm"
  DURABLE_CORE_TOOLCHAIN=$tc node --test --test-timeout=240000 test/*.test.ts || status=1
done
exit $status
