#!/usr/bin/env bash
# Regenerate chord/nextnode/plugins/node/pi: Pi's own chord sources and the two packages they run on (pi-schema, pi-telemetry), for the plugins
# component's Node subprocess (docs/plan/chord-plugins-node-evaluator.md section 7), and node/manifest.json.
# Inputs (PI_TREE, PI_COMMIT, PI_CARRIED, PI_TELEMETRY, VENDOR_CHORD_OUT): automation/gen/vendor-chord-common.sh, shared with
# vendor-chord-surfaces-client.sh.
set -euo pipefail

name=vendor-chord-plugins-node
default_out=chord/nextnode/plugins/node
# shellcheck source=automation/gen/vendor-chord-common.sh
. "$(dirname -- "${BASH_SOURCE[0]}")/vendor-chord-common.sh"

node --disable-warning=ExperimentalWarning "$root/automation/gen/vendor-chord-plugins-node.mjs" "$tree" "$commit" "$telemetry" "$out" "$pin" "$carried"
