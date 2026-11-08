#!/usr/bin/env bash
# The web pipeline's gate, the tests that need Node and a browser (they skip without it, so the default `go test` never runs them):
#   1. the embedded bundle (web/dist) is exactly what the vendored sources, the lock and the brand layer build (diff -r);
#   2. the unbranded build is Pi's bundle: its version is the one dist/bundle.json records;
#   3. build/build.test.mjs (Pi's bundles.test.ts) over the assembled tree (P2_WEB_TREE);
#   4. the Playwright e2e (e2e/ui_test.go: chat round trip, CSP and framing) with Pi's client in headless Chromium
#      (P2_UI_NODE_PATH names the node_modules holding playwright-core; Chromium comes from PLAYWRIGHT_BROWSERS_PATH, else it
#      is installed into the scratch directory).
# P2_GATE_BROWSER=0 stops after step 3 (a machine without a browser). Needs Node, npm and Go (the repository's pins) and the network for npm. usage: web/gate.sh [scratch directory]
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
app=$(dirname "$here")
scratch=${1:-$(mktemp -d)}
plugins=$(paste -sd, "$here/plugins.txt")

"$here/build/assemble.sh" "$scratch/work-upstream" "$scratch/upstream" "$plugins" upstream
version=$(node -p 'require(process.argv[1]).version' "$scratch/upstream/bundle.json")
P2_UPSTREAM_VERSION=$version "$here/build/assemble.sh" "$scratch/work" "$scratch/dist" "$plugins" pig
diff -r "$scratch/dist" "$here/dist" || { echo "web/dist is stale: run web/build/rebuild.sh" >&2; exit 1; }
[ "$(node -p 'require(process.argv[1]).brand.upstreamVersion' "$here/dist/bundle.json")" = "$version" ]

P2_WEB_TREE="$scratch/work" node --test "$here/build/build.test.mjs"

[ "${P2_GATE_BROWSER:-1}" = 0 ] && { echo "P2_GATE_BROWSER=0: browser e2e skipped"; exit 0; }
mkdir -p "$scratch/pw"
(cd "$scratch/pw" && npm init -y >/dev/null && npm i --no-audit --no-fund --loglevel=error playwright-core@1.64.1)
if [ -z "${PLAYWRIGHT_BROWSERS_PATH:-}" ]; then
  export PLAYWRIGHT_BROWSERS_PATH="$scratch/browsers"
  node "$scratch/pw/node_modules/playwright-core/cli.js" install chromium
fi
(cd "$app" && P2_UI_NODE_PATH="$scratch/pw/node_modules" go test -count=1 -run 'TestWebClient' -v ./e2e)
