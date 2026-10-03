#!/bin/sh
# Reproduces the spike. Needs the upstream 0.99.1 mirror and an install of the pinned coding-agent package (for quickjs.wasm).
# usage: run.sh <path to quickjs-wasi/quickjs.wasm> <.upstream/v0.99.1>
set -e
cp "$1" quickjs-wasi-3.6.2.wasm
node -e 'import("'"$2"'/packages/codemode/src/runtime/prelude-source.ts").then(m=>require("fs").writeFileSync("prelude.js",m.PRELUDE_SOURCE))'
go build -o spike . && go build -o spike-b ./b
mkdir -p /tmp/qjs-cache && rm -rf /tmp/qjs-cache/* && ./spike /tmp/qjs-cache && ./spike /tmp/qjs-cache
timeout 120 ./spike-b || true
