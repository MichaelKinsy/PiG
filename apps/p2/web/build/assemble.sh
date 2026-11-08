#!/usr/bin/env bash
# Assembles the tree build.mjs reads (Pi's layout: apps/p2, packages, node_modules) from the vendored files and runs it.
# usage: assemble.sh <scratch directory> <out directory> [plugins [brand]]
# brand: "upstream" (default: Pi's own bundle, byte-identical to what Pi's server builds, the D472 oracle target) or "pig" (the
# PiG brand layer, brand/brand.mjs; P2_UPSTREAM_VERSION names the matching unbranded bundle's version in bundle.json).
# Needs Node and npm (the toolchain the repository pins); installs from package-lock.json, nothing else from the network.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
web=$(dirname "$here")
scratch=$1 out=$2 plugins=${3:-} brand=${4:-upstream}
rm -rf "$scratch"
mkdir -p "$scratch/apps/p2"
cp -R "$web/upstream/." "$scratch/apps/p2/"
cp -R "$web/upstream-root/." "$scratch/"
cp "$here/package.json" "$here/package-lock.json" "$here/build.mjs" "$scratch/"
(cd "$scratch" && npm ci --ignore-scripts --no-audit --no-fund --loglevel=error)
# Pi installs marked in apps/p2/node_modules (its version differs from the hoisted one): the bundle's source map names the path.
mkdir -p "$scratch/apps/p2/node_modules"
mv "$scratch/node_modules/marked" "$scratch/apps/p2/node_modules/marked"
case $brand in
  upstream) ;;
  pig) node "$here/../brand/brand.mjs" apply "$scratch" ;;
  *) echo "unknown brand $brand" >&2; exit 2 ;;
esac
node "$scratch/build.mjs" "$scratch" "$out" $plugins
if [ "$brand" = pig ]; then node "$here/../brand/brand.mjs" finish "$out" "${P2_UPSTREAM_VERSION:?set P2_UPSTREAM_VERSION}"; fi
