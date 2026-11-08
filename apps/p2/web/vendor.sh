#!/usr/bin/env bash
# Regenerates upstream/ (Pi's apps/p2 TypeScript carried verbatim, D471) from a Pi checkout and writes MANIFEST.
# usage: vendor.sh <pi checkout> <pi commit> <list file: one apps/p2-relative path per line>
set -euo pipefail
pi=$1 commit=$2 list=$3
here=$(cd "$(dirname "$0")" && pwd)
rm -rf "$here/upstream"
mkdir -p "$here/upstream"
{
  echo "# Pi $commit apps/p2, verbatim. sha256  path (relative to apps/p2)"
  while IFS= read -r path; do
    [ -n "$path" ] || continue
    mkdir -p "$here/upstream/$(dirname "$path")"
    git -C "$pi" show "$commit:apps/p2/$path" >"$here/upstream/$path"
    printf '%s  %s\n' "$(shasum -a 256 "$here/upstream/$path" | cut -d' ' -f1)" "$path"
  done <"$list"
} >"$here/MANIFEST"
