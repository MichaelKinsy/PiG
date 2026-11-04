#!/usr/bin/env bash
# Runs Pi's catalog generator (packages/ai/catalog/generate.ts of the mirrored pin) and cmd/gen-models over the same recorded
# upstream responses and compares the provider data and public JSON byte for byte.
#
#   equivalence.sh record <fixtures-dir>            record live responses with the Go generator
#   equivalence.sh compare <fixtures-dir> [<work>]  generate with both over the fixtures and compare (work dir kept when given)
#   equivalence.sh golden <fixtures-dir> <file>     write the sha256 of every file Pi's generator writes for the fixtures
#                                                   (what cmd/gen-models' TestCatalogGenerationMatchesPi holds the Go output to)
#
# Needs `make upstream-mirror` (the Pi side runs from a copy of .upstream/current/packages/ai) and Node >= 24.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd)
cd "$root"
mode=${1:?record|compare}
fixtures=$(cd "$(dirname -- "${2:?fixtures dir}")" && pwd)/$(basename -- "$2")

case $mode in
record)
	go run ./cmd/gen-models -record "$fixtures" -strict -json-only -json-output "$(mktemp -d)/catalog"
	;;
compare|golden)
	work=${3:-$(mktemp -d)}
	[ "$mode" = golden ] && work=$(mktemp -d)
	mkdir -p "$work"
	work=$(cd "$work" && pwd)
	pi_package=$work/pi-ai
	rm -rf "$pi_package" "$work/pi" "$work/go"
	# The copy sits inside the mirror so the generator resolves the mirror's node_modules like the original does.
	mirror=$(cd .upstream/current/packages && pwd -P)
	copy=$mirror/.catalog-equivalence-$$
	mkdir "$copy"
	trap 'rm -rf "$copy"' EXIT
	for entry in package.json catalog scripts src; do cp -R "$mirror/ai/$entry" "$copy/$entry"; done
	rm -rf "$copy/src/providers/data"
	(cd "$copy" && CATALOG_FIXTURES=$fixtures node --import "$root/test/parity/testdata/catalog-generation/replay.mjs" catalog/generate.ts --strict --json-output "$work/pi/catalog" >"$work/pi.log")
	mkdir -p "$work/pi"
	cp -R "$copy/src/providers/data" "$work/pi/data"
	if [ "$mode" = golden ]; then
		(cd "$work/pi" && find . -type f ! -name .manifest.json | LC_ALL=C sort | xargs sha256sum) >"$(cd "$(dirname -- "$3")" && pwd)/$(basename -- "$3")"
		exit 0
	fi
	go run ./cmd/gen-models -replay "$fixtures" -strict -data-output "$work/go/data" -json-output "$work/go/catalog" >"$work/go.log"
	node test/parity/testdata/catalog-generation/compare.mjs "$work/pi" "$work/go"
	;;
*)
	echo "usage: $0 record|compare|golden <fixtures-dir> [<work>]" >&2
	exit 2
	;;
esac
