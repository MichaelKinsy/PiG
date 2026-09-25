#!/usr/bin/env bash
# Copy dist/utils.js from the pinned @earendil-works/pi-tui, and the
# get-east-asian-width release it depends on, into the Node extension runtime,
# so extensions measure, truncate and wrap exactly as under Pi. Run `npm ci`
# in extensions/sdk-ts first: its lockfile pins the Pi release named by
# coding.UpstreamVersion, with integrity checks. The only edit is the
# get-east-asian-width import specifier, pointed at the vendored copy.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
agent="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
package="$agent/node_modules/@earendil-works/pi-tui"
eaw="$agent/node_modules/get-east-asian-width"
version=$(node -p "require('$package/package.json').version")
shims="$root/coding/extension/host/subprocess/runtime-node/shims"
target="$shims/pi-tui-utils.mjs"
{
  printf '// Verbatim copy of dist/utils.js from @earendil-works/pi-tui@%s (MIT, Copyright (c) 2025 Mario Zechner),\n' "$version"
  printf '// importing the vendored get-east-asian-width. Regenerate with automation/gen/vendor-pi-tui-utils.sh.\n'
  sed 's#from "get-east-asian-width";#from "./get-east-asian-width/index.js";#' "$package/dist/utils.js"
} >"$target"
rm -rf "$shims/get-east-asian-width"
mkdir -p "$shims/get-east-asian-width"
cp "$eaw"/index.js "$eaw"/lookup.js "$eaw"/lookup-data.js "$eaw"/utilities.js "$eaw"/license "$eaw"/package.json "$shims/get-east-asian-width/"
echo "vendored pi-tui $version utils.js and get-east-asian-width $(node -p "require('$eaw/package.json').version") into $shims"
