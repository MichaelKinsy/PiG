#!/bin/bash
# The screens' gate captures: sessions (seeded) and empty state, three sprites, dark and light, into <out dir>.
# usage: [HEADLESS=1] run.sh <p2-desktop binary> <out dir>
#   HEADLESS=1 draws the native half with MyGo's software tester (works on a locked screen); without it the real window is
#   captured (needs an unlocked screen; the binary is the debug p2-desktop, /tmp/pd3-p2 and the seed tool for the sessions).
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
if [ "${HEADLESS:-}" != 1 ]; then
  ( cd "$HERE" && go build -o /tmp/pd3-seed ./seed )
  go build -o /tmp/pd3-p2 github.com/MichaelKinsy/PiG/apps/p2/cmd/p2
fi
for SPRITE in pig-default darth-vader pigrogu; do
  "$HERE/gate.sh" "$1" "$2" "empty-$SPRITE" "$SPRITE"
  if [ "${HEADLESS:-}" = 1 ]; then
    SEED=1 WAITTEXT="Plan the week" "$HERE/gate.sh" "$1" "$2" "sessions-$SPRITE" "$SPRITE"
  else
    SEED=/tmp/pd3-seed P2_BIN=/tmp/pd3-p2 WAITTEXT="Plan the week" "$HERE/gate.sh" "$1" "$2" "sessions-$SPRITE" "$SPRITE"
  fi
done
