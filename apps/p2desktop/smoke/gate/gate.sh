#!/bin/bash
# The visual regression gate (macOS): the native window and the BRANDED web UI of the same server (same sprite selected, same
# window size, same state), light and dark, side by side. usage: gate.sh <p2-desktop binary> <out dir> <label> <sprite id> [setup]
#   needs: /tmp/cgw (swiftc -O -o /tmp/cgw smoke/windows.swift), a Chrome for Testing in P2_CHROME_BIN, node with playwright-core (P2_UI_NODE_PATH), go.
#   WAITTEXT=<text> makes the web capture wait until the page shows that text (the seeded sessions).
# The native app and the web page are one server: the app's own loopback port, with its sprite hook (the branded bundle). Each
# run uses a scratch PIG_HOME holding the sprite selection and a scratch data directory; `setup` is a shell snippet run once the
# server is up with $PORT and $DATA set. SEED=<seed binary> (smoke/gate/seed) with P2_BIN=<p2 binary> fills the data directory
# before the app starts. The native capture waits for the app's first-frame file (P2_DESKTOP_READY_FILE: the
# sessions screen built from loaded data), never the start page; the web capture waits for the page's own settle.
set -euo pipefail
BIN=$1; OUT=$2; LABEL=$3; SPRITE=$4; SETUP=${5:-true}
export P2_CHROME_BIN=${P2_CHROME_BIN:-"$HOME/Library/Caches/ms-playwright/chromium-1212/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"}
: "${P2_UI_NODE_PATH:?P2_UI_NODE_PATH must name a node_modules directory holding playwright-core}"
export P2_UI_NODE_PATH
HERE=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$OUT"
( cd "$HERE/compose" && go build -o /tmp/pd2-compose . )
if [ "${HEADLESS:-}" = 1 ]; then
  # No window to capture (locked screen, Linux): the native half is drawn by MyGo's software tester in TestGateShot, over a
  # server that serves the branded web UI too; the page is photographed from that server. SEED=1 for the sessions.
  ( cd "$HERE/../.." && go test -c -o ${NATIVE_TEST:-/tmp/pd2-native.test} ./shell/mygoshell/native )
  for THEME in dark light; do
    HOME_=$(mktemp -d); T=$(mktemp -d); mkdir -p "$HOME_/state/pig-standard"
    [ "$SPRITE" = default ] || printf '{"variant":"%s"}\n' "$SPRITE" >"$HOME_/state/pig-standard/login.json"
    ( cd "$HERE/../../shell/mygoshell/native" && PIG_HOME=$HOME_ P2_GATE_OUT="$OUT/$LABEL-$THEME-native.png" P2_GATE_THEME=$THEME \
        P2_GATE_SEED=${SEED:+1} P2_GATE_CHAT=${CHAT:+1} P2_GATE_TOOLS=${TOOLS:+1} P2_GATE_PORT_FILE=$T/port P2_GATE_DONE_FILE=$T/done ${NATIVE_TEST:-/tmp/pd2-native.test} -test.run TestGateShot -test.count=1 >/tmp/pd2-gate.log 2>&1 ) &
    for _ in $(seq 1 1200); do [ -f "$T/port" ] && break; sleep 0.1; done
    [ -f "$T/port" ] || { echo "headless native render failed; see /tmp/pd2-gate.log"; exit 1; }
    PORT=$(sed -n 1p "$T/port"); SID=$(sed -n 2p "$T/port")
    URL=http://127.0.0.1:$PORT; [ -z "$SID" ] || URL=$URL/s/$SID
    PIG_HOME=$HOME_ node "$HERE/webshot.mjs" "$URL" "$THEME" "$OUT/$LABEL-$THEME-web.png" "${WAITTEXT:-}"
    touch "$T/done"; wait
    /tmp/pd2-compose "$OUT/$LABEL-$THEME-native.png" "$OUT/$LABEL-$THEME-web.png" "$OUT/$LABEL-$THEME-side-by-side.png" 0
    rm -rf "$HOME_" "$T"
  done
  echo "wrote $OUT/$LABEL-{dark,light}-side-by-side.png (headless native)"
  exit 0
fi
for THEME in dark light; do
  DATA=$(mktemp -d); STATE=$(mktemp -d); HOME_=$(mktemp -d); READY=$STATE/ready
  mkdir -p "$HOME_/state/pig-standard"
  [ "$SPRITE" = default ] || printf '{"variant":"%s"}\n' "$SPRITE" >"$HOME_/state/pig-standard/login.json"
  if [ -n "${SEED:-}" ]; then
    # fill the data directory first with `p2 server` (the app's first frame must already show the data: a window the
    # screen does not keep awake may stop painting)
    PIG_HOME=$HOME_ P2_DATA_DIR=$DATA P2_FAKE=1 "$P2_BIN" server >/tmp/pd2-seed.log 2>&1 &
    SPID=$!
    SPORT=""
    for _ in $(seq 1 600); do SPORT=$(sed -n 's/^p2: http:\/\/127.0.0.1:\([0-9]*\).*/\1/p' /tmp/pd2-seed.log); [ -n "$SPORT" ] && break; sleep 0.05; done
    "$SEED" "$SPORT" /tmp/pig-gate
    kill "$SPID"; wait "$SPID" 2>/dev/null || true
  fi
  PIG_HOME=$HOME_ P2_DATA_DIR=$DATA P2_FAKE=1 P2_DESKTOP_STATE_DIR=$STATE P2_DESKTOP_THEME=$THEME P2_DESKTOP_READY_FILE=$READY "$BIN" >/tmp/pd2-gate.log 2>&1 &
  PID=$!
  PORT=""
  for _ in $(seq 1 600); do
    PORT=$(sed -n 's/.*"port": *\([0-9]*\).*/\1/p' "$STATE/state.json" 2>/dev/null || true)
    [ -n "$PORT" ] && curl -sf "http://127.0.0.1:$PORT/health" >/dev/null && break
    sleep 0.05
  done
  export PORT DATA
  bash -c "$SETUP"
  for _ in $(seq 1 400); do [ -f "$READY" ] && break; sleep 0.05; done
  [ -f "$READY" ] || { echo "native first frame never came ($LABEL $THEME)"; kill "$PID"; exit 1; }
  sleep 2 # the frame (and anything the setup added) is painted
  WID=$(/tmp/cgw "$PID" | grep 'name=PiG' | sed 's/.*id=\([0-9]*\).*/\1/')
  screencapture -x -o -l "$WID" "$OUT/$LABEL-$THEME-native.png"
  # the capture is tagged with the display's profile; Chrome's screenshot is sRGB: convert so the tokens compare exactly
  sips -m "/System/Library/ColorSync/Profiles/sRGB Profile.icc" "$OUT/$LABEL-$THEME-native.png" >/dev/null
  node "$HERE/webshot.mjs" "http://127.0.0.1:$PORT" "$THEME" "$OUT/$LABEL-$THEME-web.png" "${WAITTEXT:-}"
  /tmp/pd2-compose "$OUT/$LABEL-$THEME-native.png" "$OUT/$LABEL-$THEME-web.png" "$OUT/$LABEL-$THEME-side-by-side.png"
  kill "$PID" 2>/dev/null || true
  for _ in $(seq 1 50); do kill -0 "$PID" 2>/dev/null || break; sleep 0.1; done
  kill -9 "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true
  rm -rf "$DATA" "$STATE" "$HOME_"
done
echo "wrote $OUT/$LABEL-{dark,light}-side-by-side.png"
