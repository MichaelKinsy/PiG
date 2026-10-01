#!/usr/bin/env bash
# Copy the pinned Pi runtime graphs and exact dependencies installed by the
# extensions/sdk-ts lock. This command performs no package resolution or install.
#
# shims/pi-dist/<package>/ mirrors the package's published dist directory.
# Import specifiers point at private dependency copies. The audited seams are
# D2 config paths, D74 provider API leaves, and connection-owned SDK Sessions.
# Whole coding-agent modules, docs, examples, themes and image-worker assets
# remain available for source verification and absolute package-root imports.
# bundle-pi-sdk.mjs compiles the same graph without changing its API bodies.
# pi-identity-patches.mjs replaces Pi's identity literals with PiG's (D26).
# vendor-manifest.json records package identities, input/output hashes and seams.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
agent="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
ai="$agent/node_modules/@earendil-works/pi-ai"
tui="$agent/node_modules/@earendil-works/pi-tui"
yaml="$agent/node_modules/yaml"
eaw="$agent/node_modules/get-east-asian-width"
pjson="$agent/node_modules/partial-json"
marked="$agent/node_modules/marked"
hljs="$agent/node_modules/highlight.js"
jiti="$agent/node_modules/jiti"
core="$agent/node_modules/@earendil-works/pi-agent-core"
chord="$agent/node_modules/@earendil-works/chord"
telemetry="$agent/node_modules/@earendil-works/pi-telemetry"
ignore="$agent/node_modules/ignore"
jsdiff="$agent/node_modules/diff"
shims="$root/coding/extension/host/subprocess/runtime-node/shims"
dist="$shims/pi-dist"

# copy <package dist> <target dir> <file>...: verbatim copies, keeping paths.
copy() {
  local from=$1 to=$2 file
  shift 2
  for file in "$@"; do
    mkdir -p "$to/$(dirname -- "$file")"
    cp "$from/$file" "$to/$file"
  done
}

rm -rf "$dist" "$shims/yaml" "$shims/get-east-asian-width" "$shims/partial-json" "$shims/marked" "$shims/highlight.js" "$shims/jiti" "$shims/ignore" "$shims/diff"
mkdir -p "$dist" "$shims/yaml" "$shims/get-east-asian-width" "$shims/partial-json/dist" "$shims/marked/lib" "$shims/highlight.js" "$shims/jiti" "$shims/ignore" "$shims/diff"
printf '{\n  "type": "module"\n}\n' >"$dist/package.json"

# pi-tui: every runtime module, including locally constructed screens and scroll views. Native helpers keep the package's platform/architecture layout at one of getNativeModuleCandidates' unchanged lookup locations.
while IFS= read -r file; do
  copy "$tui/dist" "$dist/pi-tui" "$file"
done < <(cd "$tui/dist" && find . -name '*.js' | sed 's#^\./##' | sort)
cp -R "$tui/native" "$dist/pi-tui/native"
sed 's#^export { Marked } from "marked";$#export { Marked } from "../../marked/lib/marked.esm.js";#' \
  "$tui/dist/index.js" >"$dist/pi-tui/index.js"
sed 's#^import { eastAsianWidth } from "get-east-asian-width";$#import { eastAsianWidth } from "../../get-east-asian-width/index.js";#' \
  "$tui/dist/utils.js" >"$dist/pi-tui/utils.js"
sed 's#^import { Marked, Tokenizer } from "marked";$#import { Marked, Tokenizer } from "../../../marked/lib/marked.esm.js";#' \
  "$tui/dist/components/markdown.js" >"$dist/pi-tui/components/markdown.js"

# pi-ai: the whole release (its compat entry is what Pi serves for the pi-ai
# root), except the builtin API implementations, which are bridge stubs.
bridged_apis="anthropic-messages azure-openai-responses bedrock-converse-stream google-generative-ai google-vertex mistral-conversations openai-codex-responses openai-completions openai-responses pi-messages"
sdk_only="google-shared"
(cd "$ai/dist" && find . \( -name '*.js' -o -path './providers/data/*.json' \) | sed 's#^\./##' | sort) | while read -r file; do
  case "$file" in
    utils/json-parse.js | utils/validation.js | utils/typebox-helpers.js | index.js | api/openrouter-images.js) continue ;;
  esac
  name=${file#api/}
  name=${name%.js}
  if [ "$file" = "api/$name.js" ] && [[ " $bridged_apis $sdk_only " == *" $name "* ]]; then
    continue
  fi
  copy "$ai/dist" "$dist/pi-ai" "$file"
done
for api in $bridged_apis; do
  printf '// PiG: the %s implementation runs in PiG'"'"'s host (D74); see automation/gen/vendor-pi-dist.sh.\nimport { bridgeApi } from "../../../pi-ai-bridge.mjs";\nexport const { stream, streamSimple } = bridgeApi("%s");\n' "$api" "$api" >"$dist/pi-ai/api/$api.js"
done
printf '// PiG: image generation has no host provider (D74); see automation/gen/vendor-pi-dist.sh.\nimport { bridgeImages } from "../../../pi-ai-bridge.mjs";\nexport const generateImages = bridgeImages("openrouter-images");\n' >"$dist/pi-ai/api/openrouter-images.js"
sed 's#^export { Type } from "typebox";$#export { Type } from "../../typebox.mjs";#' "$ai/dist/index.js" >"$dist/pi-ai/index.js"
sed 's#^import { parse as partialParse } from "partial-json";$#import { parse as partialParse } from "../../../partial-json/dist/index.js";#' \
  "$ai/dist/utils/json-parse.js" >"$dist/pi-ai/utils/json-parse.js"
sed -e 's#^import { Compile } from "typebox/compile";$#import { Compile } from "../../../typebox-compile.mjs";#' \
  -e 's#^import { Value } from "typebox/value";$#import { Value } from "../../../typebox-value.mjs";#' \
  "$ai/dist/utils/validation.js" >"$dist/pi-ai/utils/validation.js"
sed 's#^import { Type } from "typebox";$#import { Type } from "../../../typebox.mjs";#' \
  "$ai/dist/utils/typebox-helpers.js" >"$dist/pi-ai/utils/typebox-helpers.js"

# pi-coding-agent and pi-agent-core use their complete public module graphs.
node "$root/automation/gen/vendor-pi-closure.mjs" "$shims" "$(cat <<JSON
{
  "entries": ["$core/dist/index.js", "$agent/dist/index.js", "$agent/dist/core/sdk.js", "$agent/dist/utils/image-resize-worker.js"],
  "packages": {
    "@earendil-works/pi-coding-agent": { "root": "$agent", "to": "pi-dist/pi-coding-agent" },
    "@earendil-works/pi-agent-core": { "root": "$core", "to": "pi-dist/pi-agent-core" },
    "@earendil-works/chord": { "root": "$chord", "to": "pi-dist/chord" },
    "@earendil-works/pi-telemetry": { "root": "$telemetry", "to": "pi-dist/pi-telemetry" },
    "@earendil-works/pi-ai": { "root": "$ai", "to": "pi-dist/pi-ai", "vendored": true }
  },
  "overrides": {
    "@earendil-works/pi-coding-agent/index.js": "pi-coding-agent.mjs",
    "@earendil-works/pi-coding-agent/core/sdk.js": "independent-session.mjs"
  },
  "externalPrefixes": { "highlight.js/lib/": "highlight.js/lib/" },
  "external": {
    "@earendil-works/pi-tui": "pi-tui.mjs",
    "chalk": "chalk/source/index.js",
    "proper-lockfile": "proper-lockfile.mjs",
    "jiti": "jiti/lib/jiti.mjs",
    "jiti/static": "jiti/lib/jiti-static.mjs",
    "undici": "undici/index.js",
    "semver": "semver/index.js",
    "minimatch": "minimatch/dist/esm/index.js",
    "hosted-git-info": "hosted-git-info/lib/index.js",
    "grok-mermaid": "grok-mermaid/dist/index.js",
    "@silvia-odwyer/photon-node": "photon-node/photon_rs.js",
    "typebox": "typebox.mjs",
    "typebox/value": "typebox-value.mjs",
    "typebox/compile": "typebox-compile.mjs",
    "yaml": "yaml/index.js",
    "ignore": "ignore/index.js",
    "diff": "diff/libesm/index.js",
    "cross-spawn": "cross-spawn/index.js"
  }
}
JSON
)"

# Third-party packages the vendored modules import.
for dependency in chalk undici semver minimatch hosted-git-info grok-mermaid proper-lockfile; do
  rm -rf "${shims:?}/$dependency"
  node "$root/automation/gen/vendor-node-dependencies.mjs" "$agent/node_modules/$dependency/package.json" "$shims/$dependency"
done
rm -rf "$shims/photon-node"
node "$root/automation/gen/vendor-node-dependencies.mjs" "$agent/node_modules/@silvia-odwyer/photon-node/package.json" "$shims/photon-node"
node "$root/automation/gen/vendor-pi-session-seams.mjs" "$agent" "$dist/pi-coding-agent"
node "$root/automation/gen/vendor-pi-startup-seams.mjs" "$dist/pi-coding-agent"
# PiG's product identity for Pi's outbound identity literals and hosted endpoints (D26); the same values the Go host sends.
node "$root/automation/gen/pi-identity-patches.mjs" "$dist"
rm -rf "$shims/cross-spawn"
node "$root/automation/gen/vendor-node-dependencies.mjs" "$agent/node_modules/cross-spawn/package.json" "$shims/cross-spawn"
cp -R "$yaml/browser/dist" "$shims/yaml/dist"
cp "$yaml/browser/index.js" "$yaml/browser/package.json" "$yaml/LICENSE" "$shims/yaml/"
cp "$eaw"/index.js "$eaw"/lookup.js "$eaw"/lookup-data.js "$eaw"/utilities.js "$eaw"/license "$eaw"/package.json "$shims/get-east-asian-width/"
cp "$pjson/dist/index.js" "$pjson/dist/options.js" "$shims/partial-json/dist/"
cp "$pjson/package.json" "$pjson/LICENSE" "$shims/partial-json/"
cp "$marked/lib/marked.esm.js" "$shims/marked/lib/"
cp "$marked/package.json" "$marked/LICENSE" "$shims/marked/"
cp -R "$hljs/lib" "$shims/highlight.js/lib"
cp "$hljs/package.json" "$hljs/LICENSE" "$shims/highlight.js/"
cp -R "$jiti/lib" "$jiti/dist" "$shims/jiti/"
cp "$jiti/package.json" "$jiti/LICENSE" "$shims/jiti/"
cp "$ignore/index.js" "$ignore/package.json" "$ignore/LICENSE-MIT" "$shims/ignore/"
cp -R "$jsdiff/libesm" "$shims/diff/libesm"
find "$shims/diff/libesm" -name '*.d.ts' -delete -o -name '*.d.ts.map' -delete
cp "$jsdiff/package.json" "$jsdiff/LICENSE" "$shims/diff/"

# Every rewritten specifier must have matched: a pin change that alters an
# import line fails here instead of shipping a module that cannot load.
for check in \
  "$dist/pi-tui/index.js:../../marked/lib/marked.esm.js" \
  "$dist/pi-tui/utils.js:../../get-east-asian-width/index.js" \
  "$dist/pi-tui/components/markdown.js:../../../marked/lib/marked.esm.js" \
  "$dist/pi-ai/utils/json-parse.js:../../../partial-json/dist/index.js" \
  "$dist/pi-ai/utils/validation.js:../../../typebox-compile.mjs" \
  "$dist/pi-ai/utils/validation.js:../../../typebox-value.mjs" \
  "$dist/pi-ai/utils/typebox-helpers.js:../../../typebox.mjs" \
  "$dist/pi-ai/index.js:../../typebox.mjs" \
  "$dist/pi-coding-agent/utils/frontmatter.js:../../../yaml/index.js" \
  "$dist/pi-coding-agent/utils/syntax-highlight.js:../../../highlight.js/lib/core.js" \
  "$dist/pi-coding-agent/utils/syntax-highlight.js:../../../highlight.js/lib/index.js" \
  "$dist/pi-coding-agent/core/settings-manager.js:../../pi-ai/sdk-bundle/index.js" \
  "$dist/pi-coding-agent/core/settings-manager.js:../../../proper-lockfile.mjs" \
  "$dist/pi-coding-agent/core/settings-manager.js:../config.js" \
  "$dist/pi-coding-agent/modes/interactive/components/custom-editor.js:../../../../../pi-tui.mjs"; do
  grep -qF "\"${check#*:}\"" "${check%%:*}" || { echo "vendor-pi-dist: import rewrite failed in ${check%%:*}" >&2; exit 1; }
done

# Required helpers must exist in the complete copied modules.
for check in \
  "$dist/pi-coding-agent/utils/paths.js:export function resolvePath(" \
  "$dist/pi-coding-agent/core/http-dispatcher.js:export function parseHttpIdleTimeoutMs("; do
  grep -qF "${check#*:}" "${check%%:*}" || { echo "vendor-pi-dist: required helper missing in ${check%%:*}" >&2; exit 1; }
done

node "$root/automation/gen/bundle-pi-libraries.mjs" "$shims"
node "$root/automation/gen/bundle-pi-sdk.mjs" "$shims"
node "$root/automation/gen/vendor-node-manifest.mjs" "$shims" "$agent"

version() { node -p "require('$1/package.json').version"; }
echo "vendored Pi $(version "$agent") dist modules, yaml $(version "$yaml"), get-east-asian-width $(version "$eaw"), partial-json $(version "$pjson"), marked $(version "$marked"), highlight.js $(version "$hljs"), jiti $(version "$jiti"), ignore $(version "$ignore") and diff $(version "$jsdiff") into $shims"
