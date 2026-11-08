#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Builds the pinned zstd command into PREFIX (default <cache>/pig-zstd/<version>, where <cache> is $PIG_CACHE_HOME, else $XDG_CACHE_HOME, else $HOME/.cache)
# from the checksummed release source named by the zstd-source row of automation/dev/toolchains.lock, and prints the path of the executable on the last line.
# internal/zstdblob writes every embedded frame with this exact version (zstdblob.PinnedCLIVersion): the same input compresses to different bytes under another
# zstd, and GitHub's runner images carry the newest release, Homebrew another, the tern-ci image a third. The generators find the binary through PIG_ZSTD, then the
# default prefix above. The download is checked against its lock digest before it is unpacked; nothing is piped to a shell. A prefix that already holds this pin is
# reused. Building needs make and a C compiler.
#   install-zstd.sh [PREFIX]
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
lock="$root/automation/dev/toolchains.lock"

row=$(awk -F'\t' '!/^#/ && $1=="zstd-source" {print; exit}' "$lock")
if [ -z "$row" ]; then
  echo "install-zstd.sh: toolchains.lock has no zstd-source row" >&2
  exit 1
fi
version=$(cut -f2 <<<"$row") url=$(cut -f6 <<<"$row") digest=$(cut -f7 <<<"$row")
prefix=${1:-${PIG_CACHE_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}}/pig-zstd/$version}
bin="$prefix/bin/zstd"

if [ -x "$bin" ] && [ -f "$prefix/.pin" ] && [ "$(cat "$prefix/.pin")" = "$digest" ] && "$bin" --version 2>/dev/null | grep -q " v$version,"; then
  echo "$bin"; exit 0
fi

work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
"$root/automation/ci/fetch-pinned.sh" "$digest" "$work/zstd.tar.gz" "$url" >&2
tar -xzf "$work/zstd.tar.gz" -C "$work"
src="$work/zstd-$version"
# Only the command is needed: no zlib, lzma or lz4 formats, no man pages. PREFIX= is the make variable, not a path of this script's.
make -C "$src" -j"$(getconf _NPROCS 2>/dev/null || echo 2)" zstd HAVE_ZLIB=0 HAVE_LZMA=0 HAVE_LZ4=0 >"$work/make.log" 2>&1 || { cat "$work/make.log" >&2; exit 1; }
mkdir -p "$prefix/bin"
install -m 0755 "$src/programs/zstd" "$bin.$$"
mv "$bin.$$" "$bin"
printf '%s\n' "$digest" > "$prefix/.pin"
"$bin" --version | grep -q " v$version," || { echo "install-zstd.sh: the built zstd is not $version: $("$bin" --version)" >&2; exit 1; }
echo "$bin"
