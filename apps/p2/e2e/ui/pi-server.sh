#!/bin/bash
# Runs ui.mjs against Pi's own p2 server (the parity reference), inside the Playwright image on xd6702:
#   pi-server.sh <Pi checkout with node_modules and hydrated model data> <playwright-core node_modules> [script (default ui.mjs)]
# PI_PLUGINS=a,b,c starts Pi's server with exactly those plugins (default: all).
# Prints ui.mjs's JSON line (first-load bytes, server RSS) for Pi's server.
set -euo pipefail
pi=$1 modules=$2 script=${3:-ui.mjs}
docker run --rm --network host --cpus 6 --user "$(id -u):$(id -g)" -e HOME=/tmp -e SCRIPT="$script" -e PI_PLUGINS="${PI_PLUGINS:-}" -e SHOW_LOG="${SHOW_LOG:-}" \
  -e P2_UI_NODE_PATH=/pw/node_modules -e PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
  -v "$modules:/pw/node_modules:ro" -v "$pi:$pi" -v "$(cd "$(dirname "$0")" && pwd):/ui:ro" -w "$pi/apps/p2" --entrypoint "" \
  mcr.microsoft.com/playwright:v1.64.1-noble bash -c '
    export P2_FAKE=1 P2_DATA_DIR=$(mktemp -d) PORT=8791
    echo "{\"defaultModel\":{\"provider\":\"faux\",\"modelId\":\"faux-1\"},\"thinkingLevel\":\"off\"}" > $P2_DATA_DIR/settings.json
    if [ -n "$PI_PLUGINS" ]; then
      node --import ./src/source.ts --input-type=module -e "const { startServer } = await import(\"./src/hosts/server.ts\"); const s = await startServer({ plugins: process.env.PI_PLUGINS.split(\",\") }); console.log(\"p2:\", s.port)" >/tmp/pi-server.log 2>&1 &
    else
      node --import ./src/source.ts src/hosts/server.ts >/tmp/pi-server.log 2>&1 &
    fi
    pid=$!
    for _ in $(seq 60); do curl -sf http://127.0.0.1:8791/health >/dev/null && break; sleep 1; done
    node /ui/$SCRIPT http://127.0.0.1:8791 $pid || { tail -20 /tmp/pi-server.log; kill $pid; exit 1; }
    [ -z "$SHOW_LOG" ] || cat /tmp/pi-server.log
    kill $pid'
