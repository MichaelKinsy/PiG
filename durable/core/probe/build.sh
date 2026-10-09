#!/bin/sh
# Build the probe core as a WASI reactor. Usage: build.sh [out]  (default: dist/probe.wasm next to this script)
set -e
here=$(cd "$(dirname "$0")" && pwd)
out=$(realpath -m "${1:-$here/../dist/probe.wasm}")
mkdir -p "$(dirname "$out")"
cd "$here/wasm"
GOTOOLCHAIN=${GOTOOLCHAIN:-go1.27.1} GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags="-s -w" -trimpath -o "$out" .
