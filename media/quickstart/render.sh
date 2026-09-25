#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/pig-quickstart.XXXXXX")
out="$root/media/quickstart"
for tool in go vhs ttyd ffmpeg ffprobe google-chrome rg fd python3; do
  command -v "$tool" >/dev/null || { printf 'Missing recording tool: %s\n' "$tool" >&2; exit 1; }
done
# Resolve Go before changing HOME so a version-manager shim cannot install tools.
goroot=$(go env GOROOT)
gopath=$(go env GOPATH)
gocache=$(go env GOCACHE)
path="$work/home/.local/bin:$goroot/bin"
for tool in vhs ttyd ffmpeg ffprobe google-chrome rg fd; do
  path="$path:$(dirname "$(command -v "$tool")")"
done
path="$path:/usr/local/bin:/usr/bin:/bin"
mkdir -p "$work/home/.local/bin" "$work/home/demo" "$work/tmp"
printf 'Recording evidence: %s\n' "$work"
cd "$work"
env -i HOME="$work/home" PATH="$path" TMPDIR="$work/tmp" \
  GOROOT="$goroot" GOPATH="$gopath" GOCACHE="$gocache" \
  GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
  QS_SOURCE="$root" TERM=xterm-256color COLORTERM=truecolor \
  PIG_OFFLINE=1 PI_OFFLINE=1 PI_TELEMETRY=0 \
  vhs "$out/quickstart.tape" > "$work/vhs.log" 2>&1

test -s quickstart-raw.mp4
test -s quickstart.ascii
ffmpeg -y -i quickstart-raw.mp4 -an -c:v libx264 -preset slow -crf 26 \
  -pix_fmt yuv420p -movflags +faststart "$out/quickstart.mp4" > "$work/mp4.log" 2>&1
ffmpeg -y -i "$out/quickstart.mp4" \
  -filter_complex '[0:v]fps=8,split[a][b];[a]palettegen=max_colors=64:stats_mode=diff[p];[b][p]paletteuse=dither=none' \
  -loop 0 "$out/quickstart.gif" > "$work/gif.log" 2>&1
ffmpeg -y -i "$out/quickstart.mp4" -frames:v 1 -update 1 \
  "$out/quickstart-poster.png" > "$work/poster.log" 2>&1
python3 "$out/verify-media.py"
cp quickstart.ascii "$out/quickstart.ascii"
printf 'VHS screen evidence: %s/quickstart.ascii\n' "$work"
