#!/usr/bin/env bash
# Regenerates upstream/ (Pi's apps/p2 TypeScript carried verbatim, D471) from a Pi checkout and writes MANIFEST.
# usage: vendor.sh <pi checkout> <pi commit> <list file: one apps/p2-relative path per line>
# A path starting with `../../` is a file of the Pi repository outside apps/p2 (packages/*, tsconfig*.json: what the web
# build resolves through apps/p2/tsconfig.json); it is stored under upstream-root/ without the prefix.
set -euo pipefail
pi=$1 commit=$2 list=$3
here=$(cd "$(dirname "$0")" && pwd)
rm -rf "$here/upstream" "$here/upstream-root"
mkdir -p "$here/upstream"
{
  echo "# Pi $commit apps/p2, verbatim. sha256  path (relative to apps/p2)"
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    case $path in
      ../../*) source=${path#../../} target=$here/upstream-root/$source ;;
      *) source=apps/p2/$path target=$here/upstream/$path ;;
    esac
    mkdir -p "$(dirname "$target")"
    git -C "$pi" show "$commit:$source" >"$target"
    printf '%s  %s\n' "$(shasum -a 256 "$target" | cut -d' ' -f1)" "$path"
  done <"$list"
} >"$here/MANIFEST"
