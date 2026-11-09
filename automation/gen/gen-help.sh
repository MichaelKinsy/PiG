#!/usr/bin/env bash
# Render Pi's own --help with PiG's identity (Pi's piConfig name "pig" and
# configDir ".pig"), from the pinned package that extensions/sdk-ts installs.
# Lines that do not apply to a single native binary are removed below.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
pinned="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
# Resolve past version-manager shims before changing HOME.
node_bin=$(node -p 'process.execPath')
work=$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX")
output=""
# cp -R preserves the pinned package's read-only modes (a Pi build cache is read-only): the copy is patched below and removed here, so it must be writable.
trap 'chmod -R u+w "$work" 2>/dev/null || true; rm -rf "$work"; if [ -n "$output" ]; then rm -f "$output"; fi' EXIT
output=$(mktemp "$root/coding/cli/.help_upstream.XXXXXXXX")
mkdir -p "$work/node_modules/@earendil-works"
cp -R "$pinned" "$work/node_modules/@earendil-works/pi-coding-agent"
chmod -R u+w "$work/node_modules/@earendil-works/pi-coding-agent"
for dependency in "$pinned"/node_modules/*; do
  ln -s "$dependency" "$work/node_modules/$(basename "$dependency")" 2>/dev/null || true
done
"$node_bin" -e '
const fs = require("fs");
const file = process.argv[1];
const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
pkg.piConfig = { ...pkg.piConfig, name: "pig", configDir: ".pig" };
fs.writeFileSync(file, JSON.stringify(pkg, null, 2));
' "$work/node_modules/@earendil-works/pi-coding-agent/package.json"
# Pig reads PIG_PACKAGE_DIR (config.ts getPackageDir), so Pi's PI_PACKAGE_DIR line is renamed and keeps its description column.
# Pi lists its own app name as the third self-update target ('update [source|self|pi]'); pig accepts 'pig' there.
# pig divergence (D64): /share prints the PiG gateway's URL, so PI_SHARE_VIEWER_URL is unused.
# The patched app name selects PIG_CODING_AGENT_DIR, not PI_CODING_AGENT_DIR.
# Never load the invoking agent's state or let terminal color enter the artifact.
# Pi exits immediately after printing help. Node writes regular files synchronously, so capture before filtering rather than giving it a pipe that can lose queued bytes.
(
  cd "$work"
  HOME="$work/home" PIG_CODING_AGENT_DIR="$work/agent" PI_CODING_AGENT_DIR="$work/agent" \
    PI_PACKAGE_DIR="$work/node_modules/@earendil-works/pi-coding-agent" FORCE_COLOR=0 \
    "$node_bin" "$work/node_modules/@earendil-works/pi-coding-agent/dist/cli.js" --help
) >"$work/help.raw"
grep -v 'PI_SHARE_VIEWER_URL' "$work/help.raw" \
  | sed -e 's/^  PI_PACKAGE_DIR /  PIG_PACKAGE_DIR/' \
    -e 's/^  PI_SELF_UPDATE_COMMAND \(.*\)Command pi update runs to update pi itself/  PI_SELF_UPDATE_COMMAND \1Command pig update runs to update pig itself/' \
    -e 's/^  pig update \[source|self|pi\]   Update pi, extensions, or model catalogs$/  pig update [source|self|pig]   Update pig, extensions, or model catalogs/' \
  >"$output"
chmod 644 "$output"
mv "$output" "$root/coding/cli/help_upstream.txt"
echo "rendered coding/cli/help_upstream.txt from pi-coding-agent $("$node_bin" -p "require('$pinned/package.json').version")"
