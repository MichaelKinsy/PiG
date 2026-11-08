#!/bin/bash
# Runs ui.mjs against our `p2 server` binary inside the Playwright image on xd6702 (the same scenario as pi-server.sh):
#   go-server.sh <p2 binary> <playwright-core node_modules>
set -euo pipefail
bin=$(readlink -f "$1") modules=$2
docker run --rm --network host --cpus 6 --user "$(id -u):$(id -g)" -e HOME=/tmp \
  -e SESSIONS="${SESSIONS:-1}" -e SHOW_LOG="${SHOW_LOG:-}" -e SCRIPT="${SCRIPT:-ui.mjs}" -e P2_UI_TITLE="${P2_UI_TITLE:-Pi}" -e P2_UI_NODE_PATH=/pw/node_modules -e PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
  -v "$modules:/pw/node_modules:ro" -v "$bin:/p2:ro" -v "$(cd "$(dirname "$0")" && pwd):/ui:ro" --entrypoint "" \
  mcr.microsoft.com/playwright:v1.64.1-noble bash -c '
    export P2_FAKE=1 P2_DATA_DIR=$(mktemp -d) PORT=8792
    echo "{\"defaultModel\":{\"provider\":\"faux\",\"modelId\":\"faux-1\"},\"thinkingLevel\":\"off\"}" > $P2_DATA_DIR/settings.json
    /p2 server >/tmp/p2.log 2>&1 &
    pid=$!
    for _ in $(seq 60); do curl -sf http://127.0.0.1:8792/health >/dev/null && break; sleep 0.2; done
    node /ui/${SCRIPT:-ui.mjs} http://127.0.0.1:8792 $pid || { tail -20 /tmp/p2.log; kill $pid; exit 1; }
    [ -z "$SHOW_LOG" ] || cat /tmp/p2.log
    kill $pid'
