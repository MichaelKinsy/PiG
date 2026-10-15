#!/bin/sh
# Regenerates builtin-renderers.golden.json from a built Pi 1.0.4 (pi-coding-agent dist and the pi-tui.mjs shim).
# Usage: regen.sh <pi-coding-agent dist dir> <shims dir> > builtin-renderers.golden.json
set -e
dir=$(dirname "$0")
home=$(mktemp -d /tmp/hXXXXXX)
cwd=$(mktemp -d /tmp/cXXXXXX)
HOME=$home COLUMNS=80 FORCE_COLOR=3 PI_HYPERLINKS=1 PI_IMAGE_PROTOCOL=none node "$dir/render-probe.mjs" "$1" "$2" "$cwd" "$home"
