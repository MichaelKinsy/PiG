#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Downloads one release asset that CI installs, and accepts it only when its sha256 is the pinned digest.
#   fetch-pinned.sh <sha256> <destination> <url>...
# Sources, in order: (1) the digest-keyed cache (FETCH_CACHE_DIR, default ~/.cache/pig-ci-downloads, restored by actions/cache); (2) the same bytes as an
# OCI blob on ghcr.io (FETCH_OCI_REPO, default michaelkinsy/pig-ci-tools; a blob's address is its sha256, so the pin is the address; published by
# mirror-ci-tools.yml), which is not served by the release-asset endpoint that returns 504; (3) each URL, release assets. Every network fetch retries
# transient failures including HTTP 5xx (FETCH_RETRIES, default 6, with FETCH_RETRY_DELAY seconds apart, default 10). A body whose digest differs from the
# pin is discarded and the next source is tried, so no source widens what is trusted. A verified download is stored in the cache. Exit status is
# nonzero when no source yields the pinned digest.

if [ "$#" -lt 3 ]; then
  echo "usage: automation/ci/fetch-pinned.sh <sha256> <destination> <url>..." >&2
  exit 2
fi
expected=$1 destination=$2
shift 2
case "$expected" in
  *[!0-9a-f]* | "") echo "fetch-pinned.sh: '$expected' is not a lowercase sha256" >&2; exit 2 ;;
esac
if [ "${#expected}" -ne 64 ]; then
  echo "fetch-pinned.sh: '$expected' is not a 64-digit sha256" >&2
  exit 2
fi

sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

cache_dir="${FETCH_CACHE_DIR:-${HOME:-/nonexistent}/.cache/pig-ci-downloads}"
cached="$cache_dir/$expected"
if [ -f "$cached" ] && [ "$(sha256 "$cached")" = "$expected" ]; then
  cp "$cached" "$destination"
  echo "fetch-pinned.sh: using cached $expected" >&2
  exit 0
fi

store() {
  { mkdir -p "$cache_dir" && cp "$destination" "$cached.$$" && mv "$cached.$$" "$cached"; } 2>/dev/null || rm -f "$cached.$$" 2>/dev/null || true
}

retry=(--retry "${FETCH_RETRIES:-6}" --retry-all-errors --retry-delay "${FETCH_RETRY_DELAY:-10}")

# accept <label>: succeed (and cache) when the downloaded file has the pinned digest; otherwise discard it.
accept() {
  local actual
  actual=$(sha256 "$destination")
  if [ "$actual" = "$expected" ]; then
    store
    return 0
  fi
  echo "fetch-pinned.sh: $1 has sha256 $actual, pinned $expected" >&2
  rm -f "$destination"
  return 1
}

oci_repo="${FETCH_OCI_REPO-michaelkinsy/pig-ci-tools}" registry="${FETCH_OCI_REGISTRY:-ghcr.io}" scheme="${FETCH_OCI_SCHEME:-https}"
if [ -n "$oci_repo" ]; then
  if token=$(curl --fail --silent --show-error --location "${retry[@]}" "$scheme://$registry/token?service=$registry&scope=repository:$oci_repo:pull" |
    sed -n 's/.*"token" *: *"\([^"]*\)".*/\1/p') && [ -n "$token" ] &&
    curl --fail --silent --show-error --location "${retry[@]}" -H "Authorization: Bearer $token" \
      -o "$destination" "$scheme://$registry/v2/$oci_repo/blobs/sha256:$expected"; then
    accept "$registry/$oci_repo blob" && exit 0
  else
    echo "fetch-pinned.sh: $registry/$oci_repo blob sha256:$expected is unavailable" >&2
    rm -f "$destination"
  fi
fi

for url in "$@"; do
  if curl --fail --silent --show-error --location "${retry[@]}" -o "$destination" "$url"; then
    accept "$url" && exit 0
  else
    echo "fetch-pinned.sh: $url could not be downloaded" >&2
    rm -f "$destination"
  fi
done
echo "fetch-pinned.sh: no source produced the pinned sha256 $expected" >&2
exit 1
