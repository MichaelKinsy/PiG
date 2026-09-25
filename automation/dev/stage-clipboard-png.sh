#!/usr/bin/env bash
# Stage a PNG into the system clipboard so manual Ctrl+V paste tests can
# verify the pig 3.3 paste flow end-to-end.
#
# Usage:
#   ./automation/dev/stage-clipboard-png.sh path/to/image.png
#
# Platform support:
#   macOS : uses `osascript` (no extra deps).
#   Linux : needs `wl-copy` (Wayland) or `xclip` (X11). Skips with a
#            useful message if neither is on PATH.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <path-to-png>" >&2
  exit 64
fi
img="$1"
if [[ ! -f "$img" ]]; then
  echo "stage-clipboard-png: file not found: $img" >&2
  exit 1
fi

case "$(uname -s)" in
  Darwin)
    # AppleScript: read raw PNG bytes from disk into the clipboard
    # under «class PNGf» so osascript on the pig side can read it back.
    osascript <<APPLESCRIPT
set the clipboard to (read (POSIX file "$img") as «class PNGf»)
APPLESCRIPT
    echo "stage-clipboard-png: PNG copied to macOS clipboard ($img)"
    ;;
  Linux)
    if command -v wl-copy >/dev/null 2>&1 && [[ -n "${WAYLAND_DISPLAY:-}" ]]; then
      wl-copy --type image/png < "$img"
      echo "stage-clipboard-png: PNG copied via wl-copy ($img)"
    elif command -v xclip >/dev/null 2>&1 && [[ -n "${DISPLAY:-}" ]]; then
      xclip -selection clipboard -t image/png -i "$img"
      echo "stage-clipboard-png: PNG copied via xclip ($img)"
    else
      echo "stage-clipboard-png: need wl-copy (Wayland) or xclip (X11) on PATH" >&2
      exit 2
    fi
    ;;
  *)
    echo "stage-clipboard-png: unsupported platform $(uname -s)" >&2
    exit 3
    ;;
esac
