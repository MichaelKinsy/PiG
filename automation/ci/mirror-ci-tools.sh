#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Publishes the pinned CI tool tarballs as blobs of ghcr.io/michaelkinsy/pig-ci-tools, the second source of fetch-pinned.sh. A blob is addressed by its
# sha256, which is the pin, so a consumer never trusts the registry. Each asset is fetched from upstream, verified against its pin, uploaded, and the
# set is tied together by a manifest tagged "tools" so the registry keeps the blobs. Needs GHCR_TOKEN (a token with packages:write), GHCR_USER, and curl.
#   mirror-ci-tools.sh            push every pinned asset
# The package must be public for anonymous pulls (GitHub package settings, once).

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
repo="${FETCH_OCI_REPO:-michaelkinsy/pig-ci-tools}" registry=ghcr.io
sc=0.11.0
assets=(
  "8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198 https://github.com/koalaman/shellcheck/releases/download/v$sc/shellcheck-v$sc.linux.x86_64.tar.xz"
  "12b331c1d2db6b9eb13cfca64306b1b157a86eb69db83023e261eaa7e7c14588 https://github.com/koalaman/shellcheck/releases/download/v$sc/shellcheck-v$sc.linux.aarch64.tar.xz"
  "c1d593d01551f2c9a3df5ca0a0be4385a839bd9b86d4a76e18d7b17d16559127 https://github.com/google/go-containerregistry/releases/download/v0.20.6/go-containerregistry_Linux_x86_64.tar.gz"
)
# The table above must carry the pins the installers carry.
for entry in "${assets[@]}"; do
  digest=${entry%% *}
  grep -qs -- "$digest" "$root/automation/ci/install-shellcheck.sh" "$root/automation/ci/seed-ci-image-bases.sh" || { echo "mirror-ci-tools.sh: $digest is no longer pinned by the installers" >&2; exit 1; }
done
: "${GHCR_TOKEN:?GHCR_TOKEN is required}" "${GHCR_USER:?GHCR_USER is required}"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bearer=$(curl --fail --silent --show-error --retry 6 --retry-all-errors --retry-delay 10 -u "$GHCR_USER:$GHCR_TOKEN" \
  "https://$registry/token?service=$registry&scope=repository:$repo:push,pull" | sed -n 's/.*"token" *: *"\([^"]*\)".*/\1/p')
[ -n "$bearer" ] || { echo "mirror-ci-tools.sh: no registry token" >&2; exit 1; }
api() { curl --fail --silent --show-error --retry 6 --retry-all-errors --retry-delay 10 -H "Authorization: Bearer $bearer" "$@"; }

layers=""
for entry in "${assets[@]}"; do
  digest=${entry%% *} url=${entry#* } file="$work/${url##*/}"
  FETCH_OCI_REPO= FETCH_CACHE_DIR="$work/cache" "$root/automation/ci/fetch-pinned.sh" "$digest" "$file" "$url"
  size=$(wc -c <"$file" | tr -d ' ')
  location=$(api -D - -o /dev/null -X POST "https://$registry/v2/$repo/blobs/uploads/" | sed -n 's/^[Ll]ocation: *//p' | tr -d '\r')
  case "$location" in http*) ;; *) location="https://$registry$location" ;; esac
  case "$location" in *\?*) sep='&' ;; *) sep='?' ;; esac
  api -o /dev/null -X PUT -H 'Content-Type: application/octet-stream' --data-binary @"$file" "$location${sep}digest=sha256:$digest"
  layers="$layers${layers:+,}{\"mediaType\":\"application/octet-stream\",\"digest\":\"sha256:$digest\",\"size\":$size,\"annotations\":{\"org.opencontainers.image.title\":\"${url##*/}\"}}"
  echo "pushed ${url##*/} as sha256:$digest"
done
printf '{}' >"$work/config"
config_digest=$(sha256sum "$work/config" | cut -d' ' -f1)
location=$(api -D - -o /dev/null -X POST "https://$registry/v2/$repo/blobs/uploads/" | sed -n 's/^[Ll]ocation: *//p' | tr -d '\r')
case "$location" in http*) ;; *) location="https://$registry$location" ;; esac
case "$location" in *\?*) sep='&' ;; *) sep='?' ;; esac
api -o /dev/null -X PUT -H 'Content-Type: application/octet-stream' --data-binary @"$work/config" "$location${sep}digest=sha256:$config_digest"
manifest="{\"schemaVersion\":2,\"mediaType\":\"application/vnd.oci.image.manifest.v1+json\",\"artifactType\":\"application/vnd.pig.ci-tools.v1\",\"config\":{\"mediaType\":\"application/vnd.oci.empty.v1+json\",\"digest\":\"sha256:$config_digest\",\"size\":2},\"layers\":[$layers]}"
api -o /dev/null -X PUT -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' --data "$manifest" "https://$registry/v2/$repo/manifests/tools"
echo "tagged $registry/$repo:tools"
