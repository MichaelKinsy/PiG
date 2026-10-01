#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
version=$(awk -F'"' '/^const UpstreamVersion = "/ { print $2; exit }' "$root/internal/coding/pigversion/pigversion.go")
package_root=${PI_PACKAGE_ROOT:-"$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"}
ai_dist="$package_root/node_modules/@earendil-works/pi-ai/dist"

# The published catalog is a barrel (models.generated.js) that imports one shard per provider
# (providers/<provider>.models.js), and each shard imports providers/data/<provider>.json.
source="$ai_dist/models.generated.js"
[[ -f "$source" ]] || { echo "exact published Pi $version catalog missing: $source" >&2; exit 1; }
package_version=$(sed -nE 's/^[[:space:]]*"version":[[:space:]]*"([^"]+)".*/\1/p' "$ai_dist/../package.json" | head -n 1)
[[ "$package_version" == "$version" ]] || { echo "published pi-ai is $package_version, want $version: $ai_dist" >&2; exit 1; }

cd "$root"
go run ./cmd/gen-models -src "$source" -out ai/models_generated.go -image-out ai/image_models_generated.go -classifier-out ai/classifier_models_generated.go -provider-out ai/providers_generated.go
