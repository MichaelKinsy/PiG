#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Keep a saved Go build cache to the entries one job used, so a cache restored from the previous commit and saved under the
# next one does not grow with every commit.
#   age   backdate every restored entry by two hours, then record the job's start
#   trim  delete every entry the job did not read or write since `age`
# Go bumps an entry's mtime when a build reads it, at most once per hour (mtimeInterval in cmd/go/internal/cache/cache.go),
# so an entry older than an hour is bumped on its first use.

cache=$(go env GOCACHE)
case "$OSTYPE" in
  msys*|cygwin*) cache=$(cygpath -u "$cache") ;;
esac
marker="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/gocache-trim-start"

report() {
  printf 'gocache-trim %s: %s files, %s\n' "$1" "$(find "$cache" -type f | wc -l | tr -d ' ')" "$(du -sh "$cache" | cut -f1)"
}

case "${1:-}" in
  age)
    mkdir -p "$cache"
    find "$cache" -type f -exec touch -d '2 hours ago' {} +
    touch "$marker"
    report restored
    ;;
  trim)
    if [ ! -f "$marker" ]; then
      echo "gocache-trim: no start marker; run 'age' after restoring the cache" >&2
      exit 1
    fi
    report before
    find "$cache" -type f ! -newer "$marker" -delete
    find "$cache" -mindepth 1 -type d -empty -delete
    report kept
    ;;
  *)
    echo "usage: $0 age|trim" >&2
    exit 2
    ;;
esac
