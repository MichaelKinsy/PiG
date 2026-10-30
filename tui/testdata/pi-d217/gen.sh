#!/usr/bin/env bash
# Regenerate tui/testdata/pi-d217/utils.js: the pinned pi-tui src/utils.ts with the Pi fixes this release carries (D217) applied, compiled
# the way the pin build compiles it. Needs the mirror (`make upstream-mirror`) and the installed oracle (`make parity-deps`).
#
# The proof of equivalence: the UNPATCHED pin utils.ts, compiled here, must equal the built oracle's dist/utils.js byte for byte. If it
# does not, the compile setup differs from the build and the script fails without writing anything.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
mirror="$root/.upstream/current"
built="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui/dist/utils.js"
tsc="$mirror/node_modules/.bin/tsc"
# Carried hunks of packages/tui/src/utils.ts, in Pi order (git diff <sha>^ <sha> -- packages/tui/src/utils.ts).
patches=("$here/utils.ts-2ce9d527.patch")

[ -f "$built" ] || { echo "pi-d217: no built oracle at $built (make parity-deps)" >&2; exit 1; }
[ -x "$tsc" ] || { echo "pi-d217: no TypeScript at $tsc (make upstream-mirror)" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
compile() { # compile <name>: build a copy of packages/tui in $work/<name> and print the path of its dist/utils.js
	local dir="$work/$1"
	mkdir -p "$dir/packages"
	cp -R "$mirror/packages/tui" "$dir/packages/tui"
	cp "$mirror/tsconfig.base.json" "$dir/tsconfig.base.json"
	ln -s "$mirror/node_modules" "$dir/node_modules"
	rm -rf "$dir/packages/tui/dist"
	if [ "$1" = patched ]; then
		for patch in "${patches[@]}"; do (cd "$dir" && patch -s -p1 < "$patch"); done
	fi
	(cd "$dir/packages/tui" && "$tsc" -p tsconfig.build.json)
	echo "$dir/packages/tui/dist/utils.js"
}

pinned_out="$(compile pinned)"
want="$(sha256sum "$built" | cut -d' ' -f1)"
have="$(sha256sum "$pinned_out" | cut -d' ' -f1)"
if [ "$want" != "$have" ]; then
	echo "pi-d217: compiling the pinned utils.ts gives $have, the built oracle has $want; the compile setup differs from the pin build" >&2
	exit 1
fi
patched_out="$(compile patched)"
cp "$patched_out" "$here/utils.js"
printf '%s\n' "$want" > "$here/pinned-utils.sha256"
echo "pi-d217: utils.js written (pinned utils.js sha256 $want)"
