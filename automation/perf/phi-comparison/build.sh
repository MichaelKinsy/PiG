#!/bin/sh
# SPDX-License-Identifier: MIT
# Build the comparison subjects on a Linux host. Run under `taskset -c 64-95,160-191 nice -n 10` (builds are not measurements).
#   PIG_SRC=<PiG checkout> PHI_SRC=<phi checkout> OUT=<dir> build.sh
# Release binaries use each project's own release flags; the scratch pair adds harness_memclasses.go (SIGUSR2 -> Go memory
# classes) and, for pig, the HARNESS_NO_HLJS_LOAD switch in tui.LoadAllHighlightLanguages. Scratch builds are copies; the
# checkouts are never modified.
set -eu
: "${PIG_SRC:?}" "${PHI_SRC:?}" "${OUT:?}"
here=$(cd "$(dirname "$0")" && pwd)
export CGO_ENABLED=0 GOTOOLCHAIN="${GOTOOLCHAIN:-go1.27.3}"
mkdir -p "$OUT"
sha=${PIG_SHA:-$(git -C "$PIG_SRC" rev-parse --short HEAD 2>/dev/null || echo dev)}
pigflags="-s -w -X github.com/MichaelKinsy/PiG/coding/cli.Build=$sha"
(cd "$PIG_SRC" && go build -buildvcs=false -trimpath -ldflags "$pigflags" -o "$OUT/pig" ./cmd/pig)
(cd "$PHI_SRC" && go build -ldflags='-s -w' -o "$OUT/phi" ./cmd)
(cd "$PIG_SRC" && go build -ldflags="-s -w" -o "$OUT/floor" "$here/floor/main.go")
scratch="$OUT/scratch-src"
rm -rf "$scratch"; mkdir -p "$scratch"
cp -a "$PIG_SRC" "$scratch/pig"; cp -a "$PHI_SRC" "$scratch/phi"
(cd "$scratch/pig" && patch -p1 < "$here/scratch/no-hljs-load.patch" && cp "$here/scratch/harness_memclasses.go.txt" cmd/pig/harness_memclasses.go \
  && go build -buildvcs=false -trimpath -ldflags "$pigflags" -o "$OUT/pig-scratch" ./cmd/pig)
(cd "$scratch/phi" && cp "$here/scratch/harness_memclasses.go.txt" cmd/harness_memclasses.go && go build -ldflags='-s -w' -o "$OUT/phi-scratch" ./cmd)
rm -rf "$scratch"
sha256sum "$OUT"/pig "$OUT"/phi "$OUT"/pig-scratch "$OUT"/phi-scratch; ls -l "$OUT"
