#!/bin/sh
# Build one core main package as an ABI 1 WASI reactor with one toolchain.
#   build-wasm.sh <go|tinygo> <main package dir> <out.wasm>
# Both builds compile the same source. Go: wasip1 c-shared (ABI section 4). TinyGo: wasip1 c-shared with no scheduler, the
# configuration the wasm-spike measured (ADR D15); a core that needs goroutines or a scheduler does not build, which is the
# rule it must keep (ADR D1).
set -e
toolchain=${1:?go or tinygo}
pkg=${2:?main package directory}
out=$(realpath -m "${3:?output .wasm}")
mkdir -p "$(dirname "$out")"
pkg=$(realpath "$pkg")
export GOTOOLCHAIN=${GOTOOLCHAIN:-go1.27.1}
cd "$pkg"
case "$toolchain" in
  go) GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags="-s -w" -trimpath -o "$out" . ;;
  tinygo)
    tinygo=${TINYGO:-tinygo}
    command -v "$tinygo" >/dev/null 2>&1 || { echo "build-wasm.sh: tinygo not found; set TINYGO=/path/to/tinygo" >&2; exit 127; }
    "$tinygo" build -target=wasip1 -scheduler=none -buildmode=c-shared -no-debug -o "$out" . ;;
  *) echo "build-wasm.sh: unknown toolchain $toolchain" >&2; exit 2 ;;
esac
