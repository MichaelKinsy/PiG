#!/usr/bin/env bash
# Regenerates the Go catalogs from the committed catalog source (automation/gen/catalog-source), offline:
#   1. cmd/gen-models ports Pi's catalog generator and runs it over the recording (-replay-source): the provider data.
#   2. the installed oracle's barrel and provider shards (the pinned pi-ai's code) read the data Pi's OWN generator made from the same recording
#      (automation/pin/oracle.py installs it), and -src emits the Go catalogs from them.
# Step 1's data must equal the oracle's data byte for byte (manifest and generatedAt included); a difference stops here, before anything is written.
# `make model-catalogs-live` is the one target that fetches (it replaces the recording first).
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
version=$(python3 "$root/automation/pin/pin.py" get package_version)
package_root=${PI_PACKAGE_ROOT:-"$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"}
ai_dist="$package_root/node_modules/@earendil-works/pi-ai/dist"
source_dir="$root/automation/gen/catalog-source"

# The published catalog is a barrel (models.generated.js) that imports one shard per provider
# (providers/<provider>.models.js), and each shard imports providers/data/<provider>.json.
[[ -f "$ai_dist/models.generated.js" ]] || { echo "the installed Pi $version has no catalog barrel: $ai_dist/models.generated.js" >&2; exit 1; }
package_version=$(sed -nE 's/^[[:space:]]*"version":[[:space:]]*"([^"]+)".*/\1/p' "$ai_dist/../package.json" | head -n 1)
[[ "$package_version" == "$version" ]] || { echo "installed pi-ai is $package_version, want $version: $ai_dist" >&2; exit 1; }
[[ -f "$source_dir/responses.tar.gz" && -f "$source_dir/meta.json" ]] || { echo "no catalog source in $source_dir (make model-catalogs-live records one)" >&2; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$root"
go run ./cmd/gen-models -replay-source "$source_dir" -strict -data-output "$work/data" >"$work/generate.log" || { cat "$work/generate.log" >&2; exit 1; }
if ! diff -rq "$work/data" "$ai_dist/providers/data" >"$work/diff.log"; then
	echo "the provider data cmd/gen-models makes from $source_dir differs from the data Pi's generator made from it in the installed oracle (make parity-deps rebuilds the oracle):" >&2
	cat "$work/diff.log" >&2
	exit 1
fi
go run ./cmd/gen-models -src "$ai_dist/models.generated.js" -out ai/models_generated.go -image-out ai/image_models_generated.go -classifier-out ai/classifier_models_generated.go -provider-out ai/providers_generated.go
