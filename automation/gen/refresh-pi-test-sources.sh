#!/usr/bin/env bash
# Copy the verbatim Pi sources that tui/widthx/testdata/pi holds as differential-test oracles from the pinned mirror
# (.upstream/current). Run by `make pin-bump`; the list is the one in tui/widthx/testdata/pi/README.md.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
src="$root/.upstream/current/packages/tui/src"
dest="$root/tui/widthx/testdata/pi"
for file in utils.ts latex.ts terminal-image.ts components/text.ts components/truncated-text.ts components/markdown.ts; do
  cp "$src/$file" "$dest/$file"
done
echo "refreshed $dest from $(readlink "$root/.upstream/current")"
