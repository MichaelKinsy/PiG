#!/usr/bin/env bash
# Builds a `p2` binary that embeds the UNBRANDED web bundle (Pi's own, byte for byte) for the D472 oracle: Pi's UI tests assert
# Pi's texts ("Pi" as the title) and must run against the pure upstream bundle. The branded bundle stays what web/dist holds; the
# build swaps the embedded files through `go build -overlay`, so the working tree is not touched.
#   usage: upstream-binary.sh <output binary> [scratch directory]
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
web=$(dirname "$here")
app=$(dirname "$web")
out=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
scratch=${2:-$(mktemp -d)}
"$here/assemble.sh" "$scratch/work" "$scratch/upstream" "$(paste -sd, "$web/plugins.txt")" upstream
node -e '
const { readdirSync, writeFileSync, statSync } = require("node:fs");
const { join } = require("node:path");
const [dist, upstream, target] = process.argv.slice(1);
const walk = (dir) => readdirSync(dir).flatMap((name) => (statSync(join(dir, name)).isDirectory() ? walk(join(dir, name)) : [join(dir, name)]));
const replace = {};
for (const file of walk(dist)) replace[file] = ""; // the branded files leave the embedded directory
for (const name of readdirSync(upstream)) replace[join(dist, name)] = join(upstream, name);
writeFileSync(target, JSON.stringify({ Replace: replace }));
' "$web/dist" "$scratch/upstream" "$scratch/overlay.json"
(cd "$app" && go build -overlay "$scratch/overlay.json" -o "$out" ./cmd/p2)
echo "built $out with the unbranded bundle $(node -p 'require(process.argv[1]).version' "$scratch/upstream/bundle.json")"
