#!/usr/bin/env bash
# The web pipeline's gate, the tests that need Node and a browser (they skip without it, so the default `go test` never runs them):
#   1. the embedded bundle (web/dist) is exactly what the vendored sources, the lock and the brand layer build (diff -r);
#   2. the unbranded build is Pi's bundle: its version is the one dist/bundle.json records and, when P2_PI_TREE names a Pi
#      checkout at the vendored commit (node_modules installed, model data hydrated: `npm ci`, `npm run hydrate:model-data`), the
#      one Pi's own buildWebBundle makes for the same plugins; the brand overlay's texts are checked against Pi's tests there too;
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
if [ -n "${P2_PI_TREE:-}" ]; then
  commit=$(sed -n 's/^# Pi \([0-9a-f]*\) apps\/p2.*/\1/p' "$here/MANIFEST")
  [ "$(git -C "$P2_PI_TREE" rev-parse HEAD | cut -c1-${#commit})" = "$commit" ] || { echo "P2_PI_TREE is not at the vendored commit $commit" >&2; exit 1; }
  pi=$(cd "$P2_PI_TREE/apps/p2" && node --import ./src/source.ts "$here/build/pi-version.mjs" "$plugins")
  [ "$pi" = "$version" ] || { echo "Pi's buildWebBundle makes $pi, the unbranded build $version" >&2; exit 1; }
  node "$here/brand/brand.mjs" check-tests "$P2_PI_TREE" "$commit"
  echo "unbranded bundle $version is Pi's own"
else
  echo "P2_PI_TREE not set: the unbranded version was compared with the recorded one only" >&2
fi

P2_WEB_TREE="$scratch/work" node --test "$here/build/build.test.mjs"

[ "${P2_GATE_BROWSER:-1}" = 0 ] && { echo "P2_GATE_BROWSER=0: browser e2e skipped"; exit 0; }
mkdir -p "$scratch/pw"
(cd "$scratch/pw" && npm init -y >/dev/null && npm i --no-audit --no-fund --loglevel=error playwright-core@1.64.1)
if [ -z "${PLAYWRIGHT_BROWSERS_PATH:-}" ]; then
  export PLAYWRIGHT_BROWSERS_PATH="$scratch/browsers"
  # --with-deps installs system packages (apt, so root or passwordless sudo): only where that can work. A CI image without
  # either (tern-ci:v10-b9chrome) carries Chromium's system libraries already; the browser itself needs no privileges.
  deps=
  if [ -n "${CI:-}" ] && { [ "$(id -u)" = 0 ] || sudo -n true 2>/dev/null; }; then deps=--with-deps; fi
  node "$scratch/pw/node_modules/playwright-core/cli.js" install $deps chromium
fi
(cd "$app" && P2_UI_NODE_PATH="$scratch/pw/node_modules" go test -count=1 -run 'TestWebClient' -v ./e2e)
