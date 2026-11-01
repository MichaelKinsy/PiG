#!/usr/bin/env bash
# Regenerate chord/nextnode/plugins/node/pi: Pi's own chord sources and the two packages they run on, for the plugins
# component's Node subprocess (docs/plan/chord-plugins-node-evaluator.md section 7), and node/manifest.json.
#   PI_TREE        the Pi tree (default: the mirror of the pin, .upstream/current; make upstream-mirror)
#   PI_COMMIT      the commit of PI_TREE for the manifest (default: the pin's). A commit other than the pin's is a catch-up: the
#                  manifest then names it as `catchup`, the one commit the manifest may differ from the pin by (the Go test
#                  TestNodeVendoredFilesAreExactlyTheManifests reads both), and records the pin it was made against.
#   PI_TELEMETRY   the @earendil-works/pi-telemetry package dir (default: the one extensions/sdk-ts installed)
# typebox is the exact version Pi's chord pins, fetched with npm pack and checked against its registry integrity, as
# vendor-typebox.sh does; pi-telemetry must be the version Pi's chord pins too.
set -euo pipefail

version="1.3.27"
integrity="sha512-zu+jc1pcy4UiNThxikUr36f0Rybk9PEeCg/NE6adeWr/SKsdNO4EzZHYRDlv2YCVAfj3Odq3dESSo/jNyoBXzA=="
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
tree=${PI_TREE:-"$root/.upstream/current"}
pin=$(sed -n 's/^commit=//p' "$root/.upstream/pin")
commit=${PI_COMMIT:-$pin}
telemetry=${PI_TELEMETRY:-"$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-telemetry"}
out="$root/chord/nextnode/plugins/node"
work=$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX")
trap 'rm -rf "$work"' EXIT

[ -d "$tree/packages/chord/src" ] || { echo "vendor-chord-plugins-node: $tree has no packages/chord/src (make upstream-mirror, or set PI_TREE)" >&2; exit 1; }
[ -f "$telemetry/package.json" ] || { echo "vendor-chord-plugins-node: no pi-telemetry at $telemetry (install extensions/sdk-ts, or set PI_TELEMETRY)" >&2; exit 1; }
want=$(node -p "require('$tree/packages/chord/package.json').dependencies.typebox")
[ "$want" = "$version" ] || { echo "vendor-chord-plugins-node: Pi's chord pins typebox $want, this script $version" >&2; exit 1; }
wantTelemetry=$(node -p "require('$tree/packages/chord/package.json').dependencies['@earendil-works/pi-telemetry']")
haveTelemetry=$(node -p "require('$telemetry/package.json').version")
[ "$wantTelemetry" = "$haveTelemetry" ] || { echo "vendor-chord-plugins-node: Pi's chord pins @earendil-works/pi-telemetry $wantTelemetry, $telemetry is $haveTelemetry" >&2; exit 1; }

cd "$work"
npm pack --silent "typebox@$version" >/dev/null
tarball="typebox-$version.tgz"
actual="sha512-$(openssl dgst -sha512 -binary "$tarball" | base64 -w0)"
[[ "$actual" == "$integrity" ]] || { echo "typebox $version integrity mismatch: $actual" >&2; exit 1; }
mkdir typebox
tar -xzf "$tarball" -C typebox --strip-components=1
node --disable-warning=ExperimentalWarning "$root/automation/gen/vendor-chord-plugins-node.mjs" "$tree" "$commit" "$work/typebox" "$telemetry" "$out" "$pin"
