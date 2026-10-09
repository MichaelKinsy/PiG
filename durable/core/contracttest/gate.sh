#!/bin/sh
# SPDX-License-Identifier: MIT
#
# The production core's CONTRACT gate (docs/plan/durable-core/CONTRACT.md section 5, ABI section 12). Every lane runs it
# before reporting READY; the integrator runs it on every merge into dcore-integrate. It fails on the first failing stage.
#
#   1 format and vet          gofmt, go vet on durable/core
#   2 generated files         abigen -check (abi_id, abi.json)
#   3 native tests            go test ./durable/core/... (includes the purity guard)
#   4 builds                  Go wasip1 reactor and TinyGo wasip1 reactor (-scheduler=none), sizes
#   5 TinyGo unit tests       tinygo test on the packages in tinygo-packages.txt
#   6 binding conformance     native = Go Wasm = TinyGo Wasm = golden steps on every binding/testdata script
#   7 corpus                  make durable-contract (durable/contract: CONTRACT sections 5-7 against pi-durable main);
#                             runs when DCORE_CORPUS=1 (it needs make durable-contract-setup once); no row may fail
#
# Environment: TINYGO (default: tinygo on PATH), DCORE_OUT (build directory, default a temp dir), DCORE_CORPUS,
# and the durable/contract variables (CONTRACT_PI_SOURCE, DURABLE_CONTRACT_CACHE, DURABLE_CONTRACT_ARGS).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
cd "$root"
export GOTOOLCHAIN=${GOTOOLCHAIN:-go1.27.1} GOWORK=off
tinygo=${TINYGO:-$(command -v tinygo || true)}
[ -n "$tinygo" ] || { echo "gate: TinyGo not found; set TINYGO" >&2; exit 1; }
out=${DCORE_OUT:-$(mktemp -d)}
mkdir -p "$out"
stage() { printf '== %s\n' "$*"; }

stage "1 format and vet"
unformatted=$(gofmt -l durable/core)
[ -z "$unformatted" ] || { echo "gofmt: $unformatted" >&2; exit 1; }
go vet ./durable/core/...
GOOS=wasip1 GOARCH=wasm go vet ./durable/core/cmd/corewasm

stage "2 generated files"
go run ./durable/core/abi/spec/cmd/abigen -dir durable/core/abi -check

stage "3 native tests"
go test -count=1 $(go list ./durable/core/... | grep -v /durable/core/contracttest/binding$)

stage "4 builds"
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -trimpath -ldflags='-s -w' -o "$out/core-go.wasm" ./durable/core/cmd/corewasm
"$tinygo" build -target=wasip1 -buildmode=c-shared -scheduler=none -no-debug -o "$out/core-tinygo.wasm" ./durable/core/cmd/corewasm
for f in core-go core-tinygo; do
	printf '%s.wasm raw %s gzip %s\n' "$f" "$(wc -c < "$out/$f.wasm")" "$(gzip -9c "$out/$f.wasm" | wc -c)"
done

stage "5 TinyGo unit tests"
grep -v '^#' "$here/tinygo-packages.txt" | while read -r pkg; do
	[ -n "$pkg" ] || continue
	# The testing package starts goroutines, so tests use TinyGo's default scheduler; stage 4 proves the core needs none.
	"$tinygo" test "./durable/core/$pkg"
done

stage "6 binding conformance"
DCORE_WASM_GO="$out/core-go.wasm" DCORE_WASM_TINYGO="$out/core-tinygo.wasm" DCORE_REQUIRE_TINYGO=1 \
	go test -count=1 -run TestBindingConformance ./durable/core/contracttest/binding/

stage "7 corpus"
if [ "${DCORE_CORPUS:-0}" = 1 ]; then
	make durable-contract
else
	echo "skipped (DCORE_CORPUS=1 runs make durable-contract)"
fi
echo "gate: PASS"
