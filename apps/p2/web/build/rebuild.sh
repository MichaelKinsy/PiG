#!/usr/bin/env bash
# Rebuilds dist/ (the embedded web bundle) for the plugins of plugins.txt: the web facets of the plugins the p2 executable
# ships (a plugin without a Go package has no server for its web facet to talk to: Pi bundles the selected plugins' facets only).
# Needs Node and npm; run it whenever plugins.txt, upstream/ or build/ change (cmd/p2's tests fail until it was).
#   usage: rebuild.sh [scratch directory]
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
web=$(dirname "$here")
scratch=${1:-$(mktemp -d)}
"$here/assemble.sh" "$scratch" "$scratch/dist" "$(paste -sd, "$web/plugins.txt")"
rm -rf "$web/dist"
cp -R "$scratch/dist" "$web/dist"
