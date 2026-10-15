#!/bin/sh
# Builds the codemode module of the Worker host: package codemode with TinyGo as a WASI reactor with no scheduler (every import is
# synchronous; the Worker host owns the asynchrony) and capped linear memory. Usage: build-codemode-wasm.sh <out.wasm>
# The Worker also bundles quickjs-wasi's quickjs.wasm, the file codemode/assets/quickjs.wasm.
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../../../.." && pwd)
out=$(realpath -m "${1:?output .wasm}")
mkdir -p "$(dirname "$out")"
tinygo=${TINYGO:-tinygo}
command -v "$tinygo" >/dev/null 2>&1 || { echo "build-codemode-wasm.sh: tinygo not found; set TINYGO=/path/to/tinygo" >&2; exit 127; }
export GOTOOLCHAIN=${GOTOOLCHAIN:-go$(tr -d '\r\n' <"$root/.go-version")}
export BINARYEN_CORES=${BINARYEN_CORES:-8}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# The Go side holds the request, the buffered output and the messages, never the VM heap (that is QuickJS's, in the host). Measured on
# the Worker host at this module's exchange encoding: each script at the output limits (16 MiB of text, of console lines or of image
# data, or 100,000 items) grows the linear memory to 64 MiB and passes with the cap at 64 MiB; 96 MiB leaves headroom over that. The
# memory is only reserved, not used, by an execution that prints little.
printf '{\n\t"inherits": ["wasip1"],\n\t"ldflags": ["--initial-memory=1048576", "--max-memory=100663296"]\n}\n' >"$work/target.json"
cd "$root"
"$tinygo" build -target="$work/target.json" -scheduler=none -buildmode=c-shared -no-debug -o "$work/codemode.wasm" ./codemode/cloudflare/cmd/codemodewasm
mv -f "$work/codemode.wasm" "$out.$$" && mv -f "$out.$$" "$out"
