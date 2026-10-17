#!/bin/sh
# Build one core main package as an ABI 1 WASI reactor with one toolchain.
#   build-wasm.sh <go|tinygo> <main package dir> <out.wasm>
# Both builds compile the same source. Go: wasip1 c-shared (ABI section 4). TinyGo: wasip1 c-shared with no scheduler, the
# configuration the wasm-spike measured (ADR D15); a core that needs goroutines or a scheduler does not build, which is the
# rule it must keep (ADR D1).
set -e
here=$(cd "$(dirname "$0")" && pwd)
fork=$here/tinygo
toolchain=${1:?go or tinygo}
pkg=${2:?main package directory}
out=$(realpath -m "${3:?output .wasm}")
mkdir -p "$(dirname "$out")"
pkg=$(realpath "$pkg")
export GOTOOLCHAIN=${GOTOOLCHAIN:-go$(tr -d '\r\n' <"$(dirname "$0")/../../../.go-version")}
cd "$pkg"
case "$toolchain" in
  # -buildvcs=false: the module is a function of the source alone. With the VCS stamp the main module's version is a
  # pseudo-version, without it "(devel)", and the count gate's exact wasm-go counts would differ between the two.
  go) GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags="-s -w" -trimpath -buildvcs=false -o "$out" . ;;
  tinygo)
    tinygo=${TINYGO:-tinygo}
    # wasm-opt (binaryen) takes every core of the host unless told otherwise: a lane build must stay inside the lane's share.
    export BINARYEN_CORES=${BINARYEN_CORES:-8}
    command -v "$tinygo" >/dev/null 2>&1 || { echo "build-wasm.sh: tinygo not found; set TINYGO=/path/to/tinygo" >&2; exit 127; }
    # The core builds with one pinned TinyGo and a fork of a few of its runtime files (budget/tinygo, BSD-3, see its
    # README): a TINYGOROOT overlay of real directories whose files link to the toolchain's, with the forked files copied
    # in (TinyGo's GOROOT merge rejects symlinked directories). The pin is the tinygo row of automation/dev/toolchains.lock;
    # tinygo-install.sh fetches that release.
    pin=$(awk -F'\t' '!/^#/ && $1 == "tinygo" { print $2; exit }' "$here/../../../automation/dev/toolchains.lock")
    [ -n "$pin" ] || { echo "build-wasm.sh: automation/dev/toolchains.lock has no tinygo row" >&2; exit 2; }
    have=$("$tinygo" version | sed -n 's/^tinygo version \([^ ]*\) .*/\1/p')
    [ "$have" = "$pin" ] || { echo "build-wasm.sh: tinygo $have, the core's runtime fork is for $pin (tinygo-install.sh installs it)" >&2; exit 2; }
    # The heap policy is part of the build. Linear memory starts at CORE_INITIAL_MEMORY (1 MiB: the core fits at 50 and at 3,500 turns, and the memory is retained for the object's life), grows by CORE_HEAP_STEP (4 MiB; a heap smaller than the step doubles)
    # at a time in the forked runtime's growHeap, and is capped at CORE_MAX_MEMORY (96 MiB, bytes; multiples of 64 KiB), so
    # exhaustion is a trap the host recovers from, not the isolate's death. 96 MiB leaves a Durable Object's 128 MiB limit room
    # for the host's retained share (about 25 MiB of JS heap after a collection). The two memory sizes are wasm-ld flags, and
    # tinygo reaches the linker only through a target file that inherits wasip1; the step is read by the runtime from -X, and so
    # is the maximum: from two thirds of it the forked allocator keeps large free ranges for large requests (tinygo/README.md).
    ONE_CALLER_INLINE=100
    initial=${CORE_INITIAL_MEMORY:-1048576}
    max=${CORE_MAX_MEMORY:-100663296}
    step=${CORE_HEAP_STEP:-4194304}
    for v in "$initial" "$max" "$step"; do
      case "$v" in '' | *[!0-9]*) echo "build-wasm.sh: memory settings are decimal byte counts, got '$v'" >&2; exit 2 ;; esac
      [ "$v" -gt 0 ] && [ $((v % 65536)) -eq 0 ] || { echo "build-wasm.sh: $v is not a positive multiple of 64 KiB" >&2; exit 2; }
    done
    [ "$initial" -le "$max" ] || { echo "build-wasm.sh: the initial memory $initial passes the maximum $max" >&2; exit 2; }
    # CORE_OPT=<z|s|2|1>, CORE_GC=<precise|conservative> and CORE_PANIC=<print|trap> set TinyGo -opt, -gc and -panic for count experiments (the shipped build sets none: -opt=z, the precise collector, panic print); the one-caller inline limit below applies to every wasm-opt size/speed pass. CORE_PRINT_ALLOCS=<regexp> prints the escape-analysis report of the matching packages (budget/allocs.mjs reads it).
    # CORE_RUNTIME_ASSERTS=1 builds TinyGo's runtime with its GC assertions (-tags=runtime_asserts). growheap.test.mjs uses it to
    # prove the fork keeps setHeapEnd's metadata copy disjoint; the core is never shipped with it.
    case "${CORE_RUNTIME_ASSERTS:-}" in
      '') tags= ;;
      1) tags=-tags=runtime_asserts ;;
      *) echo "build-wasm.sh: CORE_RUNTIME_ASSERTS is 1 or unset, got '$CORE_RUNTIME_ASSERTS'" >&2; exit 2 ;;
    esac
    target=$(mktemp -d)
    trap 'rm -rf "$target" "$out.$$"' EXIT
    # TinyGo runs `wasm-opt -Oz`, and Binaryen inlines every function that has one caller whatever its size, undoing the
    # //go:noinline split of the core's entry points (Session.Step, dispatch and adapter.Commit came out at 20-23 KB each). V8
    # compiles a function that is hot with TurboFan at a cost that grows faster than its body, 114 ms for the first turns of a
    # session on those functions alone, so a function with one caller is inlined only when it is small (Binaryen size units).
    # TinyGo reads the wasm-opt it runs from $WASMOPT; the wrapper adds the limit to the optimizing call only. It wraps the
    # wasm-opt TinyGo would run without it: the caller's $WASMOPT, else the release's own, else the one on PATH.
    root=$("$tinygo" env TINYGOROOT)
    wasmopt=${WASMOPT:-$root/bin/wasm-opt}
    [ -x "$wasmopt" ] || wasmopt=$(command -v wasm-opt) || { echo "build-wasm.sh: no wasm-opt beside tinygo or on PATH; set WASMOPT" >&2; exit 2; }
    printf '#!/bin/sh\ncase " $* " in *" -Oz "* | *" -O2 "* | *" -Os "*) exec "%s" "$@" --one-caller-inline-max-function-size=%s ;; esac\nexec "%s" "$@"\n' "$wasmopt" "$ONE_CALLER_INLINE" "$wasmopt" > "$target/wasm-opt"
    chmod +x "$target/wasm-opt"
    export WASMOPT="$target/wasm-opt"
    cp -rs "$root" "$target/root"
    (cd "$fork" && find src -type f -name '*.go') | while read -r f; do rm -f "$target/root/$f"; cp "$fork/$f" "$target/root/$f"; done
    export TINYGOROOT="$target/root"
    # --Map: patchdata (below) takes the start of .data from the linker map.
    printf '{\n\t"inherits": ["wasip1"],\n\t"ldflags": ["--initial-memory=%s", "--max-memory=%s", "--Map=%s"]\n}\n' "$initial" "$max" "$target/core.map" > "$target/wasip1-capped.json"
    "$tinygo" build ${tags:+"$tags"} -target="$target/wasip1-capped.json" -ldflags="-X runtime.growHeapStepKiB=$((step / 1024)) -X runtime.heapMaxKiB=$((max / 1024))" -scheduler=none ${CORE_OPT:+-opt="$CORE_OPT"} ${CORE_GC:+-gc="$CORE_GC"} ${CORE_PANIC:+-panic="$CORE_PANIC"} ${CORE_PRINT_ALLOCS:+-print-allocs="$CORE_PRINT_ALLOCS"} -buildmode=c-shared -no-debug -o "$target/core.wasm" .
    # The forked findGlobals scans the globals from .data, not .rodata, once the module records where .data starts.
    (cd "$fork/patchdata" && go run . "$target/core.wasm" "$target/core.map") || exit 1
    # The functions the first turn runs are marked as compilation hints (coldset.txt names them): a host that enables hints compiles
    # them with the module instead of on their first call. The sections are inert elsewhere. The command fails when the list no
    # longer matches the module, so a renamed function cannot drop out of the hints unseen.
    # Only the production core: the probe and the test cores have other functions.
    case "$pkg" in */cmd/corewasm) (cd "$here/coldhints" && go run . ${COLDHINTS_ARGS:-} "$target/core.wasm" "$here/coldset.txt") || exit 1 ;; esac
    # The module is complete before it appears at $out, so two builds of one path (a test suite whose files each build the
    # probe on demand) never see or patch each other's half-written file (a rename within one directory is atomic).
    mv -f "$target/core.wasm" "$out.$$" && mv -f "$out.$$" "$out" ;;
  *) echo "build-wasm.sh: unknown toolchain $toolchain" >&2; exit 2 ;;
esac
