#!/bin/bash
# shots.sh <p2 binary> <playwright-core node_modules> <output directory> <label>: screenshots of `p2 server` (Playwright image).
set -euo pipefail
bin=$(readlink -f "$1") modules=$2 out=$3 label=$4
mkdir -p "$out"
docker run --rm --network host --cpus 6 --user "$(id -u):$(id -g)" -e HOME=/tmp -e LABEL="$label" \
  -e P2_UI_NODE_PATH=/pw/node_modules -e PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
  -v "$modules:/pw/node_modules:ro" -v "$bin:/p2:ro" -v "$out:/out" -v "$(cd "$(dirname "$0")" && pwd):/ui:ro" --entrypoint "" \
  mcr.microsoft.com/playwright:v1.64.1-noble bash -c '
    export P2_FAKE=1 P2_DATA_DIR=$(mktemp -d) PORT=8793
    mkdir -p /tmp/ws
    echo "{\"defaultModel\":{\"provider\":\"faux\",\"modelId\":\"faux-1\"},\"thinkingLevel\":\"off\"}" > $P2_DATA_DIR/settings.json
    /p2 server >/tmp/p2.log 2>&1 &
    pid=$!
    for _ in $(seq 60); do curl -sf http://127.0.0.1:8793/health >/dev/null && break; sleep 0.2; done
    node /ui/shots.mjs http://127.0.0.1:8793 /out "$LABEL" || { tail -20 /tmp/p2.log; kill $pid; exit 1; }
    kill $pid'
