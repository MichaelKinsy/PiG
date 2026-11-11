#!/usr/bin/env bash
# Regenerate chord/nextnode/plugins/node/pi: Pi's own chord sources and the two packages they run on (pi-schema, pi-telemetry), for the plugins
# component's Node subprocess (docs/plan/chord-plugins-node-evaluator.md section 7), and node/manifest.json.
#   PI_TREE        the Pi tree (default: the mirror of the pin, .upstream/current; make upstream-mirror)
#   PI_COMMIT      the commit of PI_TREE for the manifest (default: the pin's). A commit other than the pin's is a catch-up: the
#                  manifest then names it as `catchup`, the one commit the manifest may differ from the pin by (the Go test
#                  TestNodeVendoredFilesAreExactlyTheManifests reads both), and records the pin it was made against.
#   PI_TELEMETRY   the @earendil-works/pi-telemetry package dir (default: the one extensions/sdk-ts installed)
# @earendil-works/pi-schema is a package of the Pi tree (packages/schema), copied from PI_TREE; its version and
# pi-telemetry's must be the versions Pi's chord pins.
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
tree=${PI_TREE:-"$root/.upstream/current"}
pin=$(sed -n 's/^commit=//p' "$root/.upstream/pin")
commit=${PI_COMMIT:-$pin}
telemetry=${PI_TELEMETRY:-"$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-telemetry"}
out="$root/chord/nextnode/plugins/node"

[ -d "$tree/packages/chord/src" ] || { echo "vendor-chord-plugins-node: $tree has no packages/chord/src (make upstream-mirror, or set PI_TREE)" >&2; exit 1; }
[ -f "$telemetry/package.json" ] || { echo "vendor-chord-plugins-node: no pi-telemetry at $telemetry (install extensions/sdk-ts, or set PI_TELEMETRY)" >&2; exit 1; }
chordDeps=$(node -p "Object.keys(require('$tree/packages/chord/package.json').dependencies).sort().join(' ')")
[ "$chordDeps" = "@earendil-works/pi-schema @earendil-works/pi-telemetry" ] || { echo "vendor-chord-plugins-node: Pi's chord depends on [$chordDeps], this script carries @earendil-works/pi-schema and @earendil-works/pi-telemetry" >&2; exit 1; }
wantSchema=$(node -p "require('$tree/packages/chord/package.json').dependencies['@earendil-works/pi-schema']")
haveSchema=$(node -p "require('$tree/packages/schema/package.json').version")
[ "$wantSchema" = "$haveSchema" ] || { echo "vendor-chord-plugins-node: Pi's chord pins @earendil-works/pi-schema $wantSchema, $tree/packages/schema is $haveSchema" >&2; exit 1; }
wantTelemetry=$(node -p "require('$tree/packages/chord/package.json').dependencies['@earendil-works/pi-telemetry']")
haveTelemetry=$(node -p "require('$telemetry/package.json').version")
[ "$wantTelemetry" = "$haveTelemetry" ] || { echo "vendor-chord-plugins-node: Pi's chord pins @earendil-works/pi-telemetry $wantTelemetry, $telemetry is $haveTelemetry" >&2; exit 1; }

node --disable-warning=ExperimentalWarning "$root/automation/gen/vendor-chord-plugins-node.mjs" "$tree" "$commit" "$telemetry" "$out" "$pin"
