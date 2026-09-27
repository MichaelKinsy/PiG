#!/usr/bin/env bash
# Copy the parts of the pinned Pi release that run unchanged inside an
# extension process into the Node extension runtime, so the runtime's Pi
# modules re-export Pi's own code for them. Run `npm ci` in extensions/sdk-ts
# first: its lockfile pins the Pi release named by coding.UpstreamVersion, with
# integrity checks.
#
# shims/pi-dist/<package>/ mirrors that package's dist/ directory. Files are
# verbatim except: utils/frontmatter.js imports the vendored yaml by path, and
# core/session-manager.js keeps only its pure section (entry parsing and
# migration, context projection, from CURRENT_SESSION_VERSION through
# buildSessionContext) with the imports that section uses, importing pi-ai
# from the runtime's pi-ai module by path so the module also loads without the
# extension loader. shims/yaml/ is
# yaml's ES module build (its browser/ directory), the release Pi depends on.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
agent="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
ai="$agent/node_modules/@earendil-works/pi-ai"
yaml="$agent/node_modules/yaml"
shims="$root/coding/extension/host/subprocess/runtime-node/shims"
dist="$shims/pi-dist"

rm -rf "$dist" "$shims/yaml"
mkdir -p "$dist/pi-coding-agent/core" "$dist/pi-coding-agent/utils" "$dist/pi-ai/utils" "$shims/yaml"
printf '{\n  "type": "module"\n}\n' >"$dist/package.json"
cp "$ai/dist/utils/text.js" "$ai/dist/utils/transcript.js" "$dist/pi-ai/utils/"
cp "$agent/dist/core/messages.js" "$dist/pi-coding-agent/core/"
cp "$agent/dist/utils/text.js" "$dist/pi-coding-agent/utils/"
sed 's#^import { parse } from "yaml";$#import { parse } from "../../../yaml/index.js";#' \
  "$agent/dist/utils/frontmatter.js" >"$dist/pi-coding-agent/utils/frontmatter.js"
{
  grep -E '^import .* from "(@earendil-works/pi-ai|crypto|\./messages\.js)";$' "$agent/dist/core/session-manager.js" |
    sed 's#from "@earendil-works/pi-ai";$#from "../../../pi-ai.mjs";#'
  awk '/^export const CURRENT_SESSION_VERSION/ { on = 1 } on { print } on && /^export function buildSessionContext/ { last = 1 } last && /^}$/ { exit }' \
    "$agent/dist/core/session-manager.js"
} >"$dist/pi-coding-agent/core/session-manager.js"
cp -R "$yaml/browser/dist" "$shims/yaml/dist"
cp "$yaml/browser/index.js" "$yaml/browser/package.json" "$yaml/LICENSE" "$shims/yaml/"
echo "vendored Pi $(node -p "require('$agent/package.json').version") dist modules and yaml $(node -p "require('$yaml/package.json').version") into $shims"
