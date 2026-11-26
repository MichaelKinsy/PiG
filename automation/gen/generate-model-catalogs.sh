#!/usr/bin/env bash
# Regenerates the Go catalogs offline from the installed oracle Pi's model data:
#   - When a committed recording exists in automation/gen/catalog-source (meta.json + responses.tar.gz),
#     cmd/gen-models -replay-source must match the oracle's provider data byte for byte first.
#   - Otherwise the oracle carries the pinned Pi release's own frozen provider data
#     (automation/pin/frozen/<commit>.provider-data.tgz, verified by pin.py check-oracle), and -src
#     emits the Go catalogs directly from that exact Pi release data.
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

cd "$root"
if [[ -f "$source_dir/responses.tar.gz" && -f "$source_dir/meta.json" ]]; then
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT
	go run ./cmd/gen-models -replay-source "$source_dir" -strict -data-output "$work/data" >"$work/generate.log" || { cat "$work/generate.log" >&2; exit 1; }
	if ! diff -rq "$work/data" "$ai_dist/providers/data" >"$work/diff.log"; then
		echo "the provider data cmd/gen-models makes from $source_dir differs from the data Pi's generator made from it in the installed oracle (make parity-deps rebuilds the oracle):" >&2
		cat "$work/diff.log" >&2
		exit 1
	fi
fi
go run ./cmd/gen-models -src "$ai_dist/models.generated.js" -out ai/models_generated.go -image-out ai/image_models_generated.go -classifier-out ai/classifier_models_generated.go -provider-out ai/providers_generated.go
