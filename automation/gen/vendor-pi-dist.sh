#!/usr/bin/env bash
# Copy the parts of the pinned Pi release that run unchanged inside an
# extension process into the Node extension runtime, so the runtime's Pi
# modules re-export Pi's own code for them. Run `npm ci` in extensions/sdk-ts
# first: its lockfile pins the Pi release named by coding.UpstreamVersion, with
# integrity checks.
#
# shims/pi-dist/<package>/ mirrors that package's dist/ directory. Files are
# verbatim except for import specifiers of third-party packages, which point
# at the vendored copies by path so the modules also load without the
# extension loader:
# - pi-tui utils.js imports shims/get-east-asian-width and
#   components/markdown.js shims/marked;
# - pi-ai utils/json-parse.js imports shims/partial-json, and
#   utils/validation.js and utils/typebox-helpers.js the TypeBox bundle
#   (shims/typebox*.mjs, built by vendor-typebox.sh);
# - pi-coding-agent utils/frontmatter.js imports shims/yaml;
# - pi-coding-agent core/session-manager.js keeps only its pure section (entry
#   parsing and migration, context projection, from CURRENT_SESSION_VERSION
#   through buildSessionContext) with the imports that section uses, importing
#   pi-ai from the runtime's pi-ai module.
# shims/yaml/ is yaml's ES module build (its browser/ directory),
# shims/marked/ marked's ES module build, and shims/get-east-asian-width/ and
# shims/partial-json/ the published packages,
# each the release Pi depends on.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
agent="$root/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
ai="$agent/node_modules/@earendil-works/pi-ai"
tui="$agent/node_modules/@earendil-works/pi-tui"
yaml="$agent/node_modules/yaml"
eaw="$agent/node_modules/get-east-asian-width"
pjson="$agent/node_modules/partial-json"
marked="$agent/node_modules/marked"
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

rm -rf "$dist" "$shims/yaml" "$shims/get-east-asian-width" "$shims/partial-json" "$shims/marked"
mkdir -p "$dist" "$shims/yaml" "$shims/get-east-asian-width" "$shims/partial-json/dist" "$shims/marked/lib"
printf '{\n  "type": "module"\n}\n' >"$dist/package.json"

# pi-tui: the key parser, width utilities, keybindings and the components
# extensions construct, with the modules they import.
copy "$tui/dist" "$dist/pi-tui" \
  keys.js keybindings.js fuzzy.js kill-ring.js undo-stack.js word-navigation.js autocomplete.js \
  tui.js terminal-colors.js terminal-image.js layout-node.js latex.js stdin-buffer.js \
  components/box.js components/text.js components/spacer.js components/truncated-text.js \
  components/loader.js components/cancellable-loader.js components/settings-list.js \
  components/select-list.js components/input.js components/editor.js components/mouse-region.js \
  components/stack.js components/h-stack.js components/v-stack.js
sed 's#^import { eastAsianWidth } from "get-east-asian-width";$#import { eastAsianWidth } from "../../get-east-asian-width/index.js";#' \
  "$tui/dist/utils.js" >"$dist/pi-tui/utils.js"
sed 's#^import { Marked, Tokenizer } from "marked";$#import { Marked, Tokenizer } from "../../../marked/lib/marked.esm.js";#' \
  "$tui/dist/components/markdown.js" >"$dist/pi-tui/components/markdown.js"

# pi-ai: everything its index exports except the session-resource registry,
# whose cleanups Pi's session runs (D73), with the modules it imports.
copy "$ai/dist" "$dist/pi-ai" \
  utils/text.js utils/transcript.js utils/diagnostics.js utils/event-stream.js utils/overflow.js utils/retry.js \
  utils/assistant-message-frame.js utils/abort.js models.js models-store.js images-models.js api/lazy.js \
  auth/context.js auth/credential-store.js auth/helpers.js auth/resolve.js providers/faux.js utils/uuid.js
sed 's#^import { parse as partialParse } from "partial-json";$#import { parse as partialParse } from "../../../partial-json/dist/index.js";#' \
  "$ai/dist/utils/json-parse.js" >"$dist/pi-ai/utils/json-parse.js"
sed -e 's#^import { Compile } from "typebox/compile";$#import { Compile } from "../../../typebox-compile.mjs";#' \
  -e 's#^import { Value } from "typebox/value";$#import { Value } from "../../../typebox-value.mjs";#' \
  "$ai/dist/utils/validation.js" >"$dist/pi-ai/utils/validation.js"
sed 's#^import { Type } from "typebox";$#import { Type } from "../../../typebox.mjs";#' \
  "$ai/dist/utils/typebox-helpers.js" >"$dist/pi-ai/utils/typebox-helpers.js"

# pi-coding-agent.
copy "$agent/dist" "$dist/pi-coding-agent" core/messages.js utils/text.js
sed 's#^import { parse } from "yaml";$#import { parse } from "../../../yaml/index.js";#' \
  "$agent/dist/utils/frontmatter.js" >"$dist/pi-coding-agent/utils/frontmatter.js"
{
  grep -E '^import .* from "(@earendil-works/pi-ai|crypto|\./messages\.js)";$' "$agent/dist/core/session-manager.js" |
    sed 's#from "@earendil-works/pi-ai";$#from "../../../pi-ai.mjs";#'
  awk '/^export const CURRENT_SESSION_VERSION/ { on = 1 } on { print } on && /^export function buildSessionContext/ { last = 1 } last && /^}$/ { exit }' \
    "$agent/dist/core/session-manager.js"
} >"$dist/pi-coding-agent/core/session-manager.js"

# Third-party packages the vendored modules import.
cp -R "$yaml/browser/dist" "$shims/yaml/dist"
cp "$yaml/browser/index.js" "$yaml/browser/package.json" "$yaml/LICENSE" "$shims/yaml/"
cp "$eaw"/index.js "$eaw"/lookup.js "$eaw"/lookup-data.js "$eaw"/utilities.js "$eaw"/license "$eaw"/package.json "$shims/get-east-asian-width/"
cp "$pjson/dist/index.js" "$pjson/dist/options.js" "$shims/partial-json/dist/"
cp "$pjson/package.json" "$pjson/LICENSE" "$shims/partial-json/"
cp "$marked/lib/marked.esm.js" "$shims/marked/lib/"
cp "$marked/package.json" "$marked/LICENSE" "$shims/marked/"

# Every rewritten specifier must have matched: a pin change that alters an
# import line fails here instead of shipping a module that cannot load.
for check in \
  "$dist/pi-tui/utils.js:../../get-east-asian-width/index.js" \
  "$dist/pi-tui/components/markdown.js:../../../marked/lib/marked.esm.js" \
  "$dist/pi-ai/utils/json-parse.js:../../../partial-json/dist/index.js" \
  "$dist/pi-ai/utils/validation.js:../../../typebox-compile.mjs" \
  "$dist/pi-ai/utils/validation.js:../../../typebox-value.mjs" \
  "$dist/pi-ai/utils/typebox-helpers.js:../../../typebox.mjs" \
  "$dist/pi-coding-agent/utils/frontmatter.js:../../../yaml/index.js"; do
  grep -qF "\"${check#*:}\"" "${check%%:*}" || { echo "vendor-pi-dist: import rewrite failed in ${check%%:*}" >&2; exit 1; }
done

version() { node -p "require('$1/package.json').version"; }
echo "vendored Pi $(version "$agent") dist modules, yaml $(version "$yaml"), get-east-asian-width $(version "$eaw"), partial-json $(version "$pjson") and marked $(version "$marked") into $shims"
