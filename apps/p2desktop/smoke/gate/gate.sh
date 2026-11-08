#!/bin/bash
# The visual regression gate (macOS): the native window and the branded web UI at the same window size, same server, same
# state, light and dark, side by side. usage: gate.sh <p2-desktop binary> <out dir> <label> [setup]
#   needs: /tmp/cgw (swiftc -O -o /tmp/cgw smoke/windows.swift), a Chrome for Testing in P2_CHROME_BIN, go.
# The native app and the web page are one server: the app's own loopback port. Each run uses a scratch PIG_HOME (the default
# sprite, as the web UI's mark) and a scratch data directory; `setup` is a Go-free hook: a shell snippet run once the server
# is up with $PORT and $DATA set (it can seed sessions through p2's HTTP/Chord API or leave the data empty).
set -euo pipefail
BIN=$1; OUT=$2; LABEL=$3; SETUP=${4:-true}
CHROME=${P2_CHROME_BIN:-"$HOME/Library/Caches/ms-playwright/chromium-1212/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"}
HERE=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$OUT"
( cd "$HERE/compose" && go build -o /tmp/pd2-compose . )
for THEME in dark light; do
  DATA=$(mktemp -d); STATE=$(mktemp -d); HOME_=$(mktemp -d)
  PIG_HOME=$HOME_ P2_DATA_DIR=$DATA P2_FAKE=1 P2_DESKTOP_STATE_DIR=$STATE P2_DESKTOP_THEME=$THEME "$BIN" >/tmp/pd2-gate.log 2>&1 &
  PID=$!
  PORT=""
  for _ in $(seq 1 400); do
    PORT=$(sed -n 's/.*"port": *\([0-9]*\).*/\1/p' "$STATE/state.json" 2>/dev/null || true)
    [ -n "$PORT" ] && curl -sf "http://127.0.0.1:$PORT/health" >/dev/null && break
    sleep 0.05
  done
  export PORT DATA
  bash -c "$SETUP"
  sleep 3
  WID=$(/tmp/cgw "$PID" | grep 'name=PiG' | sed 's/.*id=\([0-9]*\).*/\1/')
  screencapture -x -o -l "$WID" "$OUT/$LABEL-$THEME-native.png"
  SCHEME=0; [ "$THEME" = dark ] && SCHEME=1
  "$CHROME" --headless=new --disable-gpu --hide-scrollbars --window-size=1280,812 --virtual-time-budget=8000 \
    --blink-settings=preferredColorScheme=$SCHEME --screenshot="$OUT/$LABEL-$THEME-web.png" "http://127.0.0.1:$PORT/" >/dev/null 2>&1
  /tmp/pd2-compose "$OUT/$LABEL-$THEME-native.png" "$OUT/$LABEL-$THEME-web.png" "$OUT/$LABEL-$THEME-side-by-side.png"
  kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true
  rm -rf "$DATA" "$STATE" "$HOME_"
done
echo "wrote $OUT/$LABEL-{dark,light}-side-by-side.png"
