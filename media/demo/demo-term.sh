#!/usr/bin/env bash
# Set HOME before bash starts and discard credentials and user startup files.
set -euo pipefail
: "${DEMO_ROOT:?Run stage.sh first}"
case "$DEMO_ROOT" in /tmp/pig-launch-*) ;; *) echo 'Expected a staged /tmp demo directory' >&2; exit 2 ;; esac
DEMO=$(cd "$(dirname "$0")" && pwd)
exec env -i HOME="$DEMO_ROOT/home" DEMO_ROOT="$DEMO_ROOT" DEMO="$DEMO" PATH=/usr/bin:/bin \
    TERM=xterm-256color LANG=C.UTF-8 /bin/bash --noprofile --rcfile "$DEMO/demo-shell.sh" -i
