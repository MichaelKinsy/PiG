#!/bin/sh
# Gates of the CF host: typecheck, then every test against the core built with each toolchain (default: go and tinygo).
# Usage: [TOOLCHAINS="go tinygo"] ./check.sh   (TinyGo: set TINYGO or put tinygo on PATH)
set -e
here=$(cd "$(dirname "$0")" && pwd)
cd "$here"
export GOTOOLCHAIN=${GOTOOLCHAIN:-go1.27.1}
node_modules/.bin/tsc -p .
for tc in ${TOOLCHAINS:-go tinygo}; do
  echo "== $tc"
  rm -f "dist/probe-$tc.wasm"
  DURABLE_CORE_TOOLCHAIN=$tc node --test --test-timeout=240000 test/*.test.ts
done
