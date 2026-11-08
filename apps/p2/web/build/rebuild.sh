#!/usr/bin/env bash
# Rebuilds dist/ (the embedded web bundle) for the plugins of plugins.txt: the web facets of the plugins the p2 executable
# ships (a plugin without a Go package has no server for its web facet to talk to: Pi bundles the selected plugins' facets only).
# dist/ is the PiG-branded build; the unbranded build (Pi's bundle, byte for byte) is made first, its version recorded in
# dist/bundle.json, and kept in $scratch/upstream for the oracle runs (D472) that must see Pi's own UI.
# Needs Node and npm; run it whenever plugins.txt, upstream/, brand/ or build/ change (cmd/p2's tests fail until it was).
#   usage: rebuild.sh [scratch directory]
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
web=$(dirname "$here")
scratch=${1:-$(mktemp -d)}
plugins=$(paste -sd, "$web/plugins.txt")
"$here/assemble.sh" "$scratch/work" "$scratch/upstream" "$plugins" upstream
version=$(node -p 'require(process.argv[1]).version' "$scratch/upstream/bundle.json")
P2_UPSTREAM_VERSION=$version "$here/assemble.sh" "$scratch/work" "$scratch/dist" "$plugins" pig
rm -rf "$web/dist"
cp -R "$scratch/dist" "$web/dist"
echo "unbranded bundle (Pi's): $scratch/upstream ($version)"
