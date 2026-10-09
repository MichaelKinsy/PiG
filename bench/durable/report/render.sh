#!/bin/sh
# Regenerates every slide from one committed results file.
#   ./render.sh           data/results.jsonl     -> final/  (refuses unless every gate in lib/results.mjs passes)
#   ./render.sh --draft   data/placeholder.jsonl -> draft/  (watermarked DRAFT)
#   ./render.sh <file> <dir>                      any results file into any directory except final/ unless final
# Chromium: $CHROME, else the newest Playwright browser in ~/.cache/ms-playwright, else chromium/google-chrome on PATH.
set -eu
cd "$(dirname "$0")"
[ -d node_modules/playwright-core ] || npm ci --no-audit --no-fund --ignore-scripts
node --test test/*.test.mjs >/dev/null || { echo "render.sh: report tests fail; run npm test" >&2; exit 1; }
case "${1:-}" in
  "") exec node build.mjs data/results.jsonl --out final ;;
  --draft) exec node build.mjs data/placeholder.jsonl --out draft ;;
  *) exec node build.mjs "$1" --out "${2:?usage: render.sh <results.jsonl> <out-dir>}" ;;
esac
