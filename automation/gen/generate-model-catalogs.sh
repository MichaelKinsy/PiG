#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
version=$(awk -F'"' '/^const UpstreamVersion = "/ { print $2; exit }' "$root/coding/pigversion/pigversion.go")
package_root=${PI_PACKAGE_ROOT:-"$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"}
ai_dist="$package_root/node_modules/@earendil-works/pi-ai/dist"

for source in "$ai_dist/models.generated.js" "$ai_dist/image-models.generated.js"; do
  [[ -f "$source" ]] || { echo "exact published Pi $version catalog missing: $source" >&2; exit 1; }
done

cd "$root"
go run ./cmd/gen-models -src "$ai_dist/models.generated.js" -out ai/models_generated.go
go run ./cmd/gen-image-models -src "$ai_dist/image-models.generated.js" -out ai/image_models_generated.go
