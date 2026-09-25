#!/usr/bin/env bash
# interface-extract-cache.sh: content-keyed cache wrapper around the TypeScript
# interface extractor used by `make interface-inventory-drift`.
#
# The extractor (parity/interface-extractor/src/extract.mjs +
# src/extract-cli.mjs) re-parses the pinned upstream source tree and the
# exact published Pi package with the TypeScript compiler on every run. Those
# inputs almost never change between candidates, so this wrapper hashes them
# and reuses a prior extraction when the hash matches, while the caller still
# always compares the (possibly cached) output against the committed
# inventories. No comparison is skipped or weakened; only the extraction step
# is cached.
#
# Usage:
#   interface-extract-cache.sh <source-root> <published-root> <upstream-version> \
#     <extractor-dir> <out-source> <out-published> <out-cli>
#
# Env:
#   PIG_INTERFACE_EXTRACT_CACHE=0   force a fresh extraction, bypassing the
#                                    cache entirely (still writes the outputs
#                                    the caller asked for).
#   PIG_CACHE_HOME                  cache root (default: $HOME/.cache); the
#                                    extracted inventories are stored under
#                                    $PIG_CACHE_HOME/pig-interface-extract/<key>/.
#   PIG_TMP                         scratch dir for temp files (default: /tmp).
set -euo pipefail

if [ $# -ne 7 ]; then
  echo "usage: interface-extract-cache.sh <source-root> <published-root> <upstream-version> <extractor-dir> <out-source> <out-published> <out-cli>" >&2
  exit 2
fi

SOURCE_ROOT=$(cd "$1" && pwd -P)
PUBLISHED_ROOT=$(cd "$2" && pwd -P)
UPSTREAM_VERSION=$3
EXTRACTOR_DIR=$(cd "$4" && pwd -P)
OUT_SOURCE=$5
OUT_PUBLISHED=$6
OUT_CLI=$7

PIG_TMP=${PIG_TMP:-/tmp}
PIG_CACHE_HOME=${PIG_CACHE_HOME:-$HOME/.cache}
CACHE_ROOT="$PIG_CACHE_HOME/pig-interface-extract"
mkdir -p "$CACHE_ROOT"

# The extraction command itself: pinning these strings in the key means a
# change to how we invoke the extractor (flags, script set) forces a fresh
# extraction even if every file on disk is unchanged.
EXTRACT_INVOCATION="extract.mjs --max-old-space-size=3072 source+published; extract-cli.mjs; cache-format=1"

hash_tree() {
  # Deterministic content hash of a directory: relative path + file content,
  # sorted by path, following symlinks (the upstream mirror and the published
  # package are both reached through symlinks from a worktree).
  local root=$1
  ( cd "$root" && find -L . -type f -print0 | sort -z | xargs -0 shasum -a 256 ) | shasum -a 256 | awk '{print $1}'
}

hash_file() {
  # Content-only hash of a single file. Reads via stdin redirection so the
  # file's (worktree-specific, absolute) path never appears in shasum's
  # output -- passing the path as an argument instead would leak it into the
  # hash ("<hash>  <path>"), making the key differ across worktrees that
  # otherwise hold byte-identical files.
  shasum -a 256 < "$1" | awk '{print $1}'
}

NODE_VERSION=$(node --version)
EXTRACTOR_SRC_HASH=$(hash_tree "$EXTRACTOR_DIR/src")
LOCK_HASH="missing"
if [ -f "$EXTRACTOR_DIR/package-lock.json" ]; then
  LOCK_HASH=$(hash_file "$EXTRACTOR_DIR/package-lock.json")
fi
SOURCE_HASH=$(hash_tree "$SOURCE_ROOT")
PUBLISHED_HASH=$(hash_tree "$PUBLISHED_ROOT")

KEY=$(printf 'node=%s\nupstream-version=%s\ninvocation=%s\nextractor-src=%s\npackage-lock=%s\nsource=%s\npublished=%s\n' \
  "$NODE_VERSION" "$UPSTREAM_VERSION" "$EXTRACT_INVOCATION" "$EXTRACTOR_SRC_HASH" "$LOCK_HASH" "$SOURCE_HASH" "$PUBLISHED_HASH" \
  | shasum -a 256 | awk '{print $1}')

CACHE_ENTRY="$CACHE_ROOT/$KEY"

run_extraction() {
  # Runs the exact same extraction the uncached recipe used to run, writing
  # into the three files under $1 (a scratch directory).
  local dest=$1
  ( cd "$EXTRACTOR_DIR" && \
    node --max-old-space-size=3072 src/extract.mjs \
      --source-root "$SOURCE_ROOT" \
      --published-root "$PUBLISHED_ROOT" \
      --upstream-version "$UPSTREAM_VERSION" \
      --source-out "$dest/source.json" \
      --published-out "$dest/published.json" && \
    node src/extract-cli.mjs \
      --source-root "$SOURCE_ROOT" \
      --upstream-version "$UPSTREAM_VERSION" \
      --out "$dest/cli.json" )
}

if [ "${PIG_INTERFACE_EXTRACT_CACHE:-1}" = "0" ]; then
  tmp=$(mktemp -d "$PIG_TMP/pig-interface-extract.XXXXXXXX")
  trap 'rm -rf "$tmp"' EXIT
  run_extraction "$tmp"
  cp "$tmp/source.json" "$OUT_SOURCE"
  cp "$tmp/published.json" "$OUT_PUBLISHED"
  cp "$tmp/cli.json" "$OUT_CLI"
  echo "interface-extract-cache: PIG_INTERFACE_EXTRACT_CACHE=0, extracted fresh (key $KEY not consulted)" >&2
  exit 0
fi

if [ -d "$CACHE_ENTRY" ] && [ -f "$CACHE_ENTRY/source.json" ] && [ -f "$CACHE_ENTRY/published.json" ] && [ -f "$CACHE_ENTRY/cli.json" ]; then
  cp "$CACHE_ENTRY/source.json" "$OUT_SOURCE"
  cp "$CACHE_ENTRY/published.json" "$OUT_PUBLISHED"
  cp "$CACHE_ENTRY/cli.json" "$OUT_CLI"
  echo "interface-extract-cache: hit $KEY" >&2
  exit 0
fi

echo "interface-extract-cache: miss $KEY, extracting" >&2
tmp=$(mktemp -d "$PIG_TMP/pig-interface-extract.XXXXXXXX")
trap 'rm -rf "$tmp"' EXIT
run_extraction "$tmp"

# Publish: claim the cache slot with a plain `mkdir`, which is an atomic
# test-and-create on every POSIX filesystem (unlike `mv` onto an existing
# directory, which silently nests instead of failing -- deliberately avoided
# here). Whoever wins the mkdir race populates the slot by writing each file
# to a hidden temp name and renaming it into place; a same-directory file
# rename is also atomic, so a concurrent reader never observes a partial
# file. Anyone who loses the mkdir race (another candidate extracting the
# identical key concurrently) just serves its own freshly computed output
# and leaves the winner's cache entry alone -- no corruption, at worst
# duplicated extraction work on that narrow race window.
publish_file() {
  local src=$1 dest_dir=$2 name=$3
  local staged
  staged=$(mktemp "$dest_dir/.$name.XXXXXXXX")
  cp "$src" "$staged"
  mv "$staged" "$dest_dir/$name"
}

if mkdir "$CACHE_ENTRY" 2>/dev/null; then
  publish_file "$tmp/source.json" "$CACHE_ENTRY" source.json
  publish_file "$tmp/published.json" "$CACHE_ENTRY" published.json
  publish_file "$tmp/cli.json" "$CACHE_ENTRY" cli.json
  echo "interface-extract-cache: published $KEY" >&2
else
  echo "interface-extract-cache: lost publish race for $KEY, used local extraction" >&2
fi

cp "$tmp/source.json" "$OUT_SOURCE"
cp "$tmp/published.json" "$OUT_PUBLISHED"
cp "$tmp/cli.json" "$OUT_CLI"
