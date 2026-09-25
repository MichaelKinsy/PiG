#!/usr/bin/env bash
# Render Pi's own --help with PiG's identity (Pi's piConfig name "pig" and
# configDir ".pig"), from the pinned package that extensions/sdk-ts installs.
# Lines that do not apply to a single native binary are removed below.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
pinned="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
work=$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/node_modules/@earendil-works"
cp -R "$pinned" "$work/node_modules/@earendil-works/pi-coding-agent"
for dependency in "$pinned"/node_modules/*; do
  ln -s "$dependency" "$work/node_modules/$(basename "$dependency")" 2>/dev/null || true
done
node -e '
const fs = require("fs");
const file = process.argv[1];
const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
pkg.piConfig = { ...pkg.piConfig, name: "pig", configDir: ".pig" };
fs.writeFileSync(file, JSON.stringify(pkg, null, 2));
' "$work/node_modules/@earendil-works/pi-coding-agent/package.json"
# PI_PACKAGE_DIR relocates the npm package's assets; the pig binary embeds them.
# D39: pig updates itself as a standalone binary, so its targets are source|self.
# pig divergence (D64): /share prints the PiG gateway's URL, so PI_SHARE_VIEWER_URL is unused.
HOME="$work/home" node "$work/node_modules/@earendil-works/pi-coding-agent/dist/cli.js" --help \
  | grep -v 'PI_PACKAGE_DIR' \
  | grep -v 'PI_SHARE_VIEWER_URL' \
  | sed 's/^  pig update \[source|self|pi\]   Update pi, extensions, or model catalogs$/  pig update [source|self]      Update pig, extensions, or model catalogs/' \
  >"$root/cmd/pig/help_upstream.txt"
echo "rendered cmd/pig/help_upstream.txt from pi-coding-agent $(node -p "require('$pinned/package.json').version")"
