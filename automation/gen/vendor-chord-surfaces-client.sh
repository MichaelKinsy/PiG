#!/usr/bin/env bash
# Regenerate chord/nextnode/surfaces/client/pi: Pi's own chord sources of the client surfaces (the page's and the terminal's runtimes, the React
# bindings) with the test fixtures the Go tests build base bundles from, and the two packages they run on (pi-schema, pi-telemetry), and
# client/manifest.json (D601).
# Inputs (PI_TREE, PI_COMMIT, PI_CARRIED, PI_TELEMETRY, VENDOR_CHORD_OUT): automation/gen/vendor-chord-common.sh, shared with
# vendor-chord-plugins-node.sh.
# React (the version Pi chord tests with, production builds) and its scheduler come from npm (`npm pack`).
set -euo pipefail

name=vendor-chord-surfaces-client
default_out=chord/nextnode/surfaces/client
# shellcheck source=automation/gen/vendor-chord-common.sh
. "$(dirname -- "${BASH_SOURCE[0]}")/vendor-chord-common.sh"

node --disable-warning=ExperimentalWarning "$root/automation/gen/vendor-chord-surfaces-client.mjs" "$tree" "$commit" "$telemetry" "$out" "$pin" "$carried"
