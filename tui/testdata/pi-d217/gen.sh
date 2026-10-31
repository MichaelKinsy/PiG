#!/usr/bin/env bash
# Regenerate the modules of tui/testdata/pi-d217: the pinned Pi's sources with the Pi fixes this release carries (D217) applied, compiled
# the way the pin build compiles them. Needs the mirror (`make upstream-mirror`) and the installed oracle (`make parity-deps`).
#
# Each module has a proof of equivalence: the UNPATCHED pin source, compiled here, must equal the built oracle's file byte for byte, and
# the vendored copy under shims/pi-dist must equal what vendor-pi-dist.sh makes of the built file. Any mismatch fails the script before
# it writes anything. manifest.json records, for each swapped module, the URL suffix, the SHA-256 of the file it replaces and the file.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
mirror="$root/.upstream/current"
agent="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
tsc="$mirror/node_modules/.bin/tsc"
dist="$root/coding/extension/host/subprocess/runtime-node/shims/pi-dist"
built_tui="$agent/node_modules/@earendil-works/pi-tui/dist/utils.js"
built_img="$agent/dist/utils/image-resize-core.js"
vendored_tui="$dist/pi-tui/utils.js"
vendored_img="$dist/pi-coding-agent/utils/image-resize-core.js"
# The vendored pi-tui utils.js is the built one with PiG's rewrites (vendor-pi-dist.sh: the get-east-asian-width import; then
# vendor-pi-startup-seams.mjs: the lazy segmenters and emoji regex). The vendored coding-agent module is the built one verbatim. For the
# patched pi-tui module the rewrites are applied as the diff from the built file to the vendored one; patch(1) must apply every hunk.
vendor_tui() { # vendor_tui <patched built utils.js>: print it with the vendoring rewrites
	local out="$work/vendor-out.js"
	cp "$1" "$out"
	patch -s --no-backup-if-mismatch "$out" <"$work/vendoring.diff" >&2
	cat "$out"
}

for f in "$built_tui" "$built_img" "$vendored_tui" "$vendored_img"; do
	[ -f "$f" ] || { echo "pi-d217: missing $f (make upstream-mirror parity-deps)" >&2; exit 1; }
done
[ -x "$tsc" ] || { echo "pi-d217: no TypeScript at $tsc (make upstream-mirror)" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
# compile <name> <package> <patch>...: build a copy of packages/<package> in $work/<name> with the patches applied (paths as in Pi:
# git diff <sha>^ <sha> -- packages/<package>/src/<file>) and print its dist directory.
compile() {
	local name=$1 pkg=$2 dir patch
	shift 2
	dir="$work/$name"
	mkdir -p "$dir/packages"
	cp -R "$mirror/packages/$pkg" "$dir/packages/$pkg"
	cp "$mirror/tsconfig.base.json" "$dir/tsconfig.base.json"
	ln -s "$mirror/node_modules" "$dir/node_modules"
	rm -rf "$dir/packages/$pkg/dist"
	for patch in "$@"; do (cd "$dir" && patch -s -p1 < "$patch"); done
	(cd "$dir/packages/$pkg" && "$tsc" -p tsconfig.build.json) >&2
	echo "$dir/packages/$pkg/dist"
}
same() { # same <what> <file> <file>
	[ "$(sha256sum "$2" | cut -d' ' -f1)" = "$(sha256sum "$3" | cut -d' ' -f1)" ] || { echo "pi-d217: $1 differs ($2 vs $3); the compile setup or the vendoring is not the build's" >&2; exit 1; }
}

tui_pinned="$(compile tui-pinned tui)"
same "pinned pi-tui utils.js compiled here against the built oracle" "$tui_pinned/utils.js" "$built_tui"
diff -u "$built_tui" "$vendored_tui" >"$work/vendoring.diff" || true
[ -s "$work/vendoring.diff" ] || { echo "pi-d217: the vendored pi-tui utils.js equals the built one; the vendoring is not what this script expects" >&2; exit 1; }
vendor_tui "$built_tui" >"$work/vendored-pinned.js"
same "the vendored pi-tui utils.js against the vendoring rewrites applied to the built one" "$work/vendored-pinned.js" "$vendored_tui"
img_pinned="$(compile img-pinned coding-agent)"
same "pinned image-resize-core.js compiled here against the built oracle" "$img_pinned/utils/image-resize-core.js" "$built_img"
same "the vendored image-resize-core.js against the built one" "$built_img" "$vendored_img"

tui_patched="$(compile tui-patched tui "$here/utils.ts-2ce9d527.patch")"
img_patched="$(compile img-patched coding-agent "$here/image-resize-core.ts-e7a0df27.patch")"
cp "$tui_patched/utils.js" "$here/utils.js"
vendor_tui "$tui_patched/utils.js" >"$here/utils.vendored.js"
cp "$img_patched/utils/image-resize-core.js" "$here/image-resize-core.js"
sha() { sha256sum "$1" | cut -d' ' -f1; }
cat >"$here/manifest.json" <<JSON
[
  { "suffix": "/@earendil-works/pi-tui/dist/utils.js", "pinnedSha256": "$(sha "$built_tui")", "file": "utils.js" },
  { "suffix": "/packages/tui/dist/utils.js", "pinnedSha256": "$(sha "$built_tui")", "file": "utils.js" },
  { "suffix": "/pi-dist/pi-tui/utils.js", "pinnedSha256": "$(sha "$vendored_tui")", "file": "utils.vendored.js" },
  { "suffix": "/pi-dist/pi-coding-agent/utils/image-resize-core.js", "pinnedSha256": "$(sha "$vendored_img")", "file": "image-resize-core.js" }
]
JSON
echo "pi-d217: modules and manifest.json written"
