#!/usr/bin/env bash
# Runs PiG's extension conformance suite on the compiled aot host.
#
# The suite writes its extensions per test, so it runs on a dynamic host: the aot runtime's executable with jiti compiled in and no extension compiled in. That
# exercises the runtime tree, the harness and the wire on the compiled executable; extensions compiled in are covered by piglets/aot's end-to-end tests.
#
# Needs PIG_AOT_TEST_COMPILER (a Pi-Bolt 0.7.4 runtime executable). Extra arguments go to `go test` after the package, for example -run Node.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
: "${PIG_AOT_TEST_COMPILER:?set PIG_AOT_TEST_COMPILER to the Pi-Bolt runtime executable}"
work="$(mktemp -d "${TMPDIR:-/tmp}/aot-conformance.XXXXXX")"
trap 'rm -rf "$work"' EXIT
(
	cd "$root/piglets/aot"
	GOWORK=off PIG_AOT_CONFORMANCE_SLOT="$work/slot" PIG_AOT_TEST_RUNTIME_TREE="$root/coding/extension/host/subprocess/runtime-node" \
		go test ./extensions/aotruntime -run '^TestWriteConformanceSlot$' -count=1
)
test -x "$work/slot/aot/pig-ts-host" || { echo "the conformance slot has no compiled host" >&2; exit 1; }
cd "$root"
# The suite falls back to Node without a word when the slot's host does not verify or start, so check that the request resolves to the compiled host.
go build -o "$work/pig" ./cmd/pig
mkdir -p "$work/home"
selected="$(HOME="$work/home" PIG_CODING_AGENT_DIR="$work/home/agent" PIG_TS_RUNTIME=aot PIG_TS_RUNTIMES_DIR="$work/slot" "$work/pig" extensions runtime status | grep '^Selected:')"
case "$selected" in
*"aot (executable"*) ;;
*) echo "the conformance slot does not select the compiled host: $selected" >&2; exit 1 ;;
esac
PIG_TS_RUNTIME=aot PIG_TS_RUNTIMES_DIR="$work/slot" go test ./test/extension-conformance -count=1 -timeout 50m "$@"
