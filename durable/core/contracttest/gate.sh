#!/bin/sh
# SPDX-License-Identifier: MIT
#
# The production core's CONTRACT gate (docs/plan/durable-core/CONTRACT.md section 5, ABI section 12). Every lane runs it
# before reporting READY; the integrator runs it on every merge into dcore-integrate. It fails on the first failing stage.
#
#   1 format and vet          gofmt, go vet on durable/core
#   2 generated files         abigen -check (abi_id, abi.json)
#   3 native tests            go test ./durable/core/... (includes the purity guard and the import boundary:
#                             contracttest/boundary fails if a core package reaches encoding/json, reflect or text/template)
#   4 builds                  Go wasip1 reactor and TinyGo wasip1 reactor (-scheduler=none, fixed-step heap, memory ceiling), sizes
#   5 TinyGo unit tests       tinygo test on the packages in tinygo-packages.txt
#   6 binding conformance     native = Go Wasm = TinyGo Wasm = golden steps on every binding/testdata script
#   7 corpus                  make durable-contract (durable/contract: CONTRACT sections 5-7 against pi-durable main);
#                             runs when DCORE_CORPUS=1 (it needs make durable-contract-setup once); no row may fail
#   8 count budgets           make durable-counts (durable/contract/cli/counts.mjs against counts.json); runs when DCORE_CORPUS=1,
#                             with VALGRIND naming the pinned valgrind (durable/contract/tools/build-valgrind.sh)
#
# GATE_BUILDS selects the builds the gate builds and runs (owner decision, questions/dcore-yield.md 20:50): the default
# "tinygo native" is the two shipped builds and runs on every merge; "all" (or "tinygo go native") adds standard Go Wasm
# and the bench engine for the nightly run and before a release. Without "go", stage 7 re-runs the rows TinyGo failed on
# Go Wasm as a non-gating diagnostic. A stage-6 comparison still builds the Go module itself: it is the reference of the
# step goldens, not a corpus build.
#
# Environment: GATE_BUILDS, TINYGO (default: tinygo on PATH), DCORE_OUT (build directory, default a temp dir), DCORE_CORPUS,
# and the durable/contract variables (CONTRACT_PI_SOURCE, DURABLE_CONTRACT_CACHE, DURABLE_CONTRACT_ARGS).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../../.." && pwd)
cd "$root"
# The programs build through the tree's go.work, as the Makefile's gates do (an empty GOWORK selects it).
export GOTOOLCHAIN=${GOTOOLCHAIN:-go$(tr -d '\r\n' <"$root/.go-version")} GOWORK=
builds=${GATE_BUILDS:-"tinygo native"}
[ "$builds" != all ] || builds="tinygo go native"
for b in $builds; do
	case $b in tinygo | go | native) ;; *) echo "gate: GATE_BUILDS entry '$b' is not tinygo, go or native (or all)" >&2; exit 2 ;; esac
done
selected() { case " $builds " in *" $1 "*) return 0 ;; esac; return 1; }
tinygo=${TINYGO:-$(command -v tinygo || true)}
if selected tinygo && [ -z "$tinygo" ]; then echo "gate: TinyGo not found; set TINYGO" >&2; exit 1; fi
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

stage "4 builds ($builds)"
built=
if selected go; then
	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -trimpath -ldflags='-s -w' -o "$out/core-go.wasm" ./durable/core/cmd/corewasm
	built="$built core-go"
fi
if selected tinygo; then
	# The build the corpus runs: the pinned TinyGo with the fixed-step heap and the memory ceiling (budget/build-wasm.sh). wasm-opt
	# (binaryen) takes every core of the host unless told otherwise.
	TINYGO="$tinygo" BINARYEN_CORES=${BINARYEN_CORES:-8} sh durable/core/budget/build-wasm.sh tinygo durable/core/cmd/corewasm "$out/core-tinygo.wasm"
	built="$built core-tinygo"
fi
for f in $built; do
	printf '%s.wasm raw %s gzip %s\n' "$f" "$(wc -c < "$out/$f.wasm")" "$(gzip -9c "$out/$f.wasm" | wc -c)"
done

stage "5 TinyGo unit tests"
if selected tinygo; then
	grep -v '^#' "$here/tinygo-packages.txt" | while read -r pkg; do
		[ -n "$pkg" ] || continue
		# The testing package starts goroutines, so tests use TinyGo's default scheduler; stage 4 proves the core needs none.
		"$tinygo" test "./durable/core/$pkg"
	done
else
	echo "skipped (TinyGo is not in GATE_BUILDS)"
fi

stage "6 binding conformance"
if selected tinygo; then
	if selected go; then
		DCORE_WASM_GO="$out/core-go.wasm" DCORE_WASM_TINYGO="$out/core-tinygo.wasm" DCORE_REQUIRE_TINYGO=1 \
			go test -count=1 -run TestBindingConformance ./durable/core/contracttest/binding/
	else
		DCORE_WASM_TINYGO="$out/core-tinygo.wasm" DCORE_REQUIRE_TINYGO=1 \
			go test -count=1 -run TestBindingConformance ./durable/core/contracttest/binding/
	fi
else
	go test -count=1 -run TestBindingConformance ./durable/core/contracttest/binding/
fi

stage "7 corpus"
if [ "${DCORE_CORPUS:-0}" = 1 ]; then
	impls=pi
	selected tinygo && impls="$impls,wasm-tinygo"
	selected go && impls="$impls,wasm-go"
	selected native && impls="$impls,native"
	[ "$builds" != "tinygo go native" ] || impls="$impls,native-bench"
	case " ${DURABLE_CONTRACT_ARGS:-} " in
	*" --impls "*) make durable-contract ;;
	*) make durable-contract DURABLE_CONTRACT_ARGS="--impls $impls ${DURABLE_CONTRACT_ARGS:-}" ;;
	esac
else
	echo "skipped (DCORE_CORPUS=1 runs make durable-contract)"
fi
stage "8 count budgets"
if [ "${DCORE_CORPUS:-0}" = 1 ]; then
	# Rows, crossings, heap, linear memory, allocations, instructions and live-set growth of the shipped builds, against
	# durable/contract/counts.json (a ratchet: a number over its baseline fails, and so does one under it until it is lowered).
	make durable-counts
	# The Durable 0.5.0 bar's memory rules over the same numbers: no per-turn live-set growth, heap independent of length.
	make durable-bar
else
	echo "skipped (DCORE_CORPUS=1 runs make durable-counts)"
fi
echo "gate: PASS"
