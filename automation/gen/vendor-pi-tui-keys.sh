#!/usr/bin/env bash
# Copy dist/keys.js from the pinned @earendil-works/pi-tui into the Node
# extension runtime. Run `npm ci` in extensions/sdk-ts first: its lockfile pins
# the Pi release named by coding.UpstreamVersion, with integrity checks.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
package="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui"
version=$(node -p "require('$package/package.json').version")
target="$root/coding/extension/host/subprocess/runtime-node/shims/pi-tui-keys.mjs"
{
  printf '// Verbatim copy of dist/keys.js from @earendil-works/pi-tui@%s (MIT, Copyright (c) 2025 Mario Zechner).\n' "$version"
  printf '// Regenerate with automation/gen/vendor-pi-tui-keys.sh; runtime_node_vendored_test.go checks it against the pinned package.\n'
  cat "$package/dist/keys.js"
} >"$target"
echo "vendored pi-tui $version keys.js into $target"
